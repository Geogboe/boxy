// Package pki generates and loads the private certificate authority used
// to secure agent<->server gRPC connections (see
// docs/adr/0005-remote-agent-transport-and-registration.md). This is
// deliberately not a public CA (Let's Encrypt etc.) — that's a separate,
// out-of-scope concern reserved for the web dashboard. Boxy mints its own
// CA so agent<->server traffic gets real TLS encryption and full mutual
// authentication without depending on any external certificate authority.
//
// This package is public (moved from internal/pki, see issue #138): it is
// stdlib-only with zero dependencies on the rest of boxy, so it's a clean
// seam for reuse outside this module. Its API is not yet stable — boxy is
// prerelease and this package may still change shape without notice.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"maps"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const (
	caCertFileName     = "ca.crt"
	caKeyFileName      = "ca.key"
	serverCertFileName = "server.crt"
	serverKeyFileName  = "server.key"

	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 397 * 24 * time.Hour // matches common public-CA max leaf lifetime
	agentValidity  = 397 * 24 * time.Hour
)

// CA is boxy's private certificate authority: a self-signed root used to
// sign the server's leaf certificate and every agent's client certificate.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
}

// ServerCert is the gRPC listener's leaf certificate, signed by the CA.
type ServerCert struct {
	CertPEM []byte
	KeyPEM  []byte
}

// EnsureCA loads dir/ca.crt and dir/ca.key if both are present, or
// generates a new self-signed CA and writes them if not. Safe to call on
// every `boxy serve` startup.
func EnsureCA(dir string) (*CA, error) {
	certPath := filepath.Join(dir, caCertFileName)
	keyPath := filepath.Join(dir, caKeyFileName)

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		return parseCA(certPEM, keyPEM)
	}
	if !os.IsNotExist(certErr) && certErr != nil {
		return nil, fmt.Errorf("read CA cert %q: %w", certPath, certErr)
	}
	if !os.IsNotExist(keyErr) && keyErr != nil {
		return nil, fmt.Errorf("read CA key %q: %w", keyPath, keyErr)
	}

	ca, err := generateCA()
	if err != nil {
		return nil, fmt.Errorf("generate CA: %w", err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %q: %w", dir, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(ca.Key)
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyOut, 0o600); err != nil {
		return nil, fmt.Errorf("write CA key %q: %w", keyPath, err)
	}
	if err := os.WriteFile(certPath, ca.CertPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write CA cert %q: %w", certPath, err)
	}

	return ca, nil
}

func generateCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := newSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "boxy internal CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse generated certificate: %w", err)
	}

	return &CA{
		Cert:    cert,
		Key:     key,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}, nil
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("decode CA cert PEM: no PEM block found")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("decode CA key PEM: no PEM block found")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}

	return &CA{Cert: cert, Key: key, CertPEM: certPEM}, nil
}

// IssueServerCert loads dir/server.crt and dir/server.key if both are
// present and their SANs already match sans (compared as an
// order-independent set of DNS names/IPs), or (re)issues a new leaf
// certificate — signed by ca, valid for sans — and writes/overwrites both
// files otherwise. This means changing the configured SANs (e.g. via a new
// --grpc-cert-san / server.grpc_cert_sans entry) takes effect automatically
// on the next call — no manual deletion required. dir/server.crt and
// dir/server.key are entirely boxy-managed (see
// docs/adr/0005-remote-agent-transport-and-registration.md); if the
// existing cert can't be parsed, IssueServerCert fails rather than
// silently overwriting it — delete both files by hand and call again to
// recover.
func IssueServerCert(ca *CA, dir string, sans []string) (*ServerCert, error) {
	certPath := filepath.Join(dir, serverCertFileName)
	keyPath := filepath.Join(dir, serverKeyFileName)

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		reused, err := reuseServerCertIfSANsMatch(certPath, certPEM, keyPEM, sans)
		if err != nil {
			return nil, err
		}
		if reused != nil {
			return reused, nil
		}
		// SANs changed since the cert on disk was issued: fall through and
		// regenerate below.
	case !os.IsNotExist(certErr) && certErr != nil:
		return nil, fmt.Errorf("read server cert %q: %w", certPath, certErr)
	case !os.IsNotExist(keyErr) && keyErr != nil:
		return nil, fmt.Errorf("read server key %q: %w", keyPath, keyErr)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate server key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "boxy server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	applySANs(tmpl, sans)

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, fmt.Errorf("create server certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal server key: %w", err)
	}

	sc := &ServerCert{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %q: %w", dir, err)
	}
	if err := os.WriteFile(keyPath, sc.KeyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write server key %q: %w", keyPath, err)
	}
	if err := os.WriteFile(certPath, sc.CertPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write server cert %q: %w", certPath, err)
	}

	return sc, nil
}

// reuseServerCertIfSANsMatch parses the server cert already on disk
// (certPEM) and compares its SANs against sans as an order-independent
// set. If they match, it returns the existing cert/key pair unchanged
// (preserving IssueServerCert's idempotent, no-churn behavior across
// restarts with unchanged config). If they differ, it returns (nil, nil) —
// the caller should regenerate. If certPEM can't be parsed as a
// certificate, it returns an error rather than treating that as "differs":
// a corrupt/unparseable file is exactly the situation where the caller
// knows least about what's on disk, so IssueServerCert fails loudly
// instead of silently overwriting it.
func reuseServerCertIfSANsMatch(certPath string, certPEM, keyPEM []byte, sans []string) (*ServerCert, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("existing server certificate %q is invalid: no PEM block found (delete %q and its .key file and call again to regenerate)", certPath, certPath)
	}
	existing, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("existing server certificate %q is invalid: %w (delete %q and its .key file and call again to regenerate)", certPath, err, certPath)
	}

	wantDNS, wantIPs := sanSets(sans)
	haveDNS, haveIPs := certSANSets(existing)
	if maps.Equal(wantDNS, haveDNS) && maps.Equal(wantIPs, haveIPs) {
		return &ServerCert{CertPEM: certPEM, KeyPEM: keyPEM}, nil
	}

	slog.Info("regenerating agent gRPC server certificate: SANs changed",
		"path", certPath,
		"old_dns", slices.Sorted(maps.Keys(haveDNS)),
		"old_ips", slices.Sorted(maps.Keys(haveIPs)),
		"new_dns", slices.Sorted(maps.Keys(wantDNS)),
		"new_ips", slices.Sorted(maps.Keys(wantIPs)),
	)
	return nil, nil
}

// sanSets splits sans the same way applySANs does, returning comparable
// sets keyed by normalized string form. IPs are normalized via
// net.IP.String() rather than compared as raw bytes/strings — required so
// they compare equal to the sets certSANSets extracts from an already
// round-tripped x509 certificate (see certSANSets).
func sanSets(sans []string) (dns map[string]struct{}, ips map[string]struct{}) {
	dns = make(map[string]struct{})
	ips = make(map[string]struct{})
	for _, san := range sans {
		if ip := net.ParseIP(san); ip != nil {
			ips[ip.String()] = struct{}{}
		} else {
			dns[san] = struct{}{}
		}
	}
	return dns, ips
}

// certSANSets extracts the same comparable sets from an already-parsed
// certificate's DNSNames/IPAddresses. IPs are normalized via
// net.IP.String(): x509.ParseCertificate narrows IPv4 addresses to their
// 4-byte form on round-trip, so comparing raw net.IP byte slices (or
// net.ParseIP's 16-byte output) against cert.IPAddresses would spuriously
// report "changed" on every restart even when the configured SANs haven't
// changed — String() sidesteps the encoding difference entirely.
func certSANSets(cert *x509.Certificate) (dns map[string]struct{}, ips map[string]struct{}) {
	dns = make(map[string]struct{})
	for _, name := range cert.DNSNames {
		dns[name] = struct{}{}
	}
	ips = make(map[string]struct{})
	for _, ip := range cert.IPAddresses {
		ips[ip.String()] = struct{}{}
	}
	return dns, ips
}

// IssueAgentCert mints a fresh client certificate for a newly registered
// agent, signed by ca. Unlike EnsureCA/IssueServerCert, this is never
// written to disk server-side: the private key is generated ephemerally,
// returned once (over the already-authenticated registration stream) for
// the agent to persist on its own host, and the server retains only the
// serial number (for future revocation, see pkg/store's
// RevokedAgentIdentity) — never the agent's private key.
func IssueAgentCert(ca *CA, agentID string) (certPEM, keyPEM []byte, serial string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("generate agent key: %w", err)
	}
	serialNum, err := newSerial()
	if err != nil {
		return nil, nil, "", err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serialNum,
		Subject:      pkix.Name{CommonName: agentID},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(agentValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, "", fmt.Errorf("create agent certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal agent key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, serialNum.String(), nil
}

func applySANs(tmpl *x509.Certificate, sans []string) {
	for _, san := range sans {
		if ip := net.ParseIP(san); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, san)
		}
	}
}

func newSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial number: %w", err)
	}
	return serial, nil
}
