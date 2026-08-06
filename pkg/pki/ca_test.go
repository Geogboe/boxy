package pki

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCA_GeneratesAndReloads(t *testing.T) {
	dir := t.TempDir()

	ca1, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA (generate): %v", err)
	}
	if !ca1.Cert.IsCA {
		t.Fatal("expected generated cert to be a CA")
	}

	ca2, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA (reload): %v", err)
	}
	if ca1.Cert.SerialNumber.Cmp(ca2.Cert.SerialNumber) != 0 {
		t.Fatal("expected reloading an existing CA to return the same certificate, not generate a new one")
	}
}

func TestIssueServerCert_ChainValidatesAgainstCA(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	sc, err := IssueServerCert(ca, dir, []string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM)

	block, _ := pem.Decode(sc.CertPEM)
	if block == nil {
		t.Fatal("expected a PEM block in the server cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse server cert: %v", err)
	}

	if _, err := cert.Verify(x509.VerifyOptions{
		DNSName: "localhost",
		Roots:   pool,
	}); err != nil {
		t.Fatalf("server cert did not verify against the CA: %v", err)
	}

	// TLS key/cert pair must actually load together.
	if _, err := tls.X509KeyPair(sc.CertPEM, sc.KeyPEM); err != nil {
		t.Fatalf("server cert/key did not form a valid TLS key pair: %v", err)
	}
}

// TestIssueServerCert_Idempotent asserts that calling IssueServerCert twice
// with the *same* SANs reloads the existing cert rather than minting a new
// one. This still holds under the SAN-diff logic (reuseServerCertIfSANsMatch
// short-circuits when the requested and on-disk SAN sets compare equal) —
// it's now also the regression guard for the IPv4-byte-length pitfall
// documented on certSANSets: because this test uses "127.0.0.1", a bug that
// compared raw net.IP bytes instead of normalized strings would make this
// test spuriously fail (report "changed" and regenerate every time).
func TestIssueServerCert_Idempotent(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	sc1, err := IssueServerCert(ca, dir, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueServerCert (first): %v", err)
	}
	sc2, err := IssueServerCert(ca, dir, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueServerCert (second): %v", err)
	}
	if string(sc1.CertPEM) != string(sc2.CertPEM) {
		t.Fatal("expected a second IssueServerCert call with unchanged SANs to reload the existing cert, not mint a new one")
	}
}

// TestIssueServerCert_StableWhenSANsReordered guards the "order-independent
// set" comparison requirement: the same two SANs passed in a different
// order must still be treated as unchanged (a naive ordered-slice or
// joined-string comparison would regenerate here).
func TestIssueServerCert_StableWhenSANsReordered(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	sc1, err := IssueServerCert(ca, dir, []string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatalf("IssueServerCert (first): %v", err)
	}
	sc2, err := IssueServerCert(ca, dir, []string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueServerCert (second, reordered): %v", err)
	}
	if string(sc1.CertPEM) != string(sc2.CertPEM) {
		t.Fatal("expected reordering the same SANs to reload the existing cert, not mint a new one")
	}
}

// TestIssueServerCert_RegeneratesWhenSANsChange is the core acceptance test
// for issue #147: when the requested SAN set differs from what's on disk,
// IssueServerCert must regenerate rather than silently keeping the stale
// cert.
func TestIssueServerCert_RegeneratesWhenSANsChange(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	sc1, err := IssueServerCert(ca, dir, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueServerCert (first): %v", err)
	}
	sc2, err := IssueServerCert(ca, dir, []string{"127.0.0.1", "extra.example.test"})
	if err != nil {
		t.Fatalf("IssueServerCert (second, extra SAN): %v", err)
	}
	if string(sc1.CertPEM) == string(sc2.CertPEM) {
		t.Fatal("expected adding a SAN to regenerate the cert, not reuse the stale one")
	}

	block, _ := pem.Decode(sc2.CertPEM)
	if block == nil {
		t.Fatal("expected a PEM block in the regenerated server cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse regenerated server cert: %v", err)
	}
	found := false
	for _, name := range cert.DNSNames {
		if name == "extra.example.test" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected regenerated cert DNSNames to contain %q, got %v", "extra.example.test", cert.DNSNames)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM)
	if _, err := cert.Verify(x509.VerifyOptions{DNSName: "extra.example.test", Roots: pool}); err != nil {
		t.Fatalf("regenerated cert did not verify against the CA for the new SAN: %v", err)
	}
	if _, err := tls.X509KeyPair(sc2.CertPEM, sc2.KeyPEM); err != nil {
		t.Fatalf("regenerated cert/key did not form a valid TLS key pair: %v", err)
	}
}

// TestIssueServerCert_ErrorsWhenExistingCertUnparseable confirms the
// fail-loudly decision: a corrupt/unparseable server.crt must cause
// IssueServerCert to return an error, not silently self-heal by
// regenerating (which could paper over something more concerning than a
// stale SAN list).
func TestIssueServerCert_ErrorsWhenExistingCertUnparseable(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	// A valid key but garbage "certificate" bytes.
	if _, err := IssueServerCert(ca, dir, []string{"127.0.0.1"}); err != nil {
		t.Fatalf("seed IssueServerCert: %v", err)
	}
	certPath := filepath.Join(dir, serverCertFileName)
	if err := os.WriteFile(certPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("corrupt server.crt: %v", err)
	}

	keyPath := filepath.Join(dir, serverKeyFileName)
	before, statErr := os.ReadFile(keyPath)
	if statErr != nil {
		t.Fatalf("read server key before call: %v", statErr)
	}

	_, err = IssueServerCert(ca, dir, []string{"127.0.0.1"})
	if err == nil {
		t.Fatal("expected IssueServerCert to error on an unparseable existing certificate, not self-heal")
	}

	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read server key after call: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("expected IssueServerCert to leave server.key untouched when failing loudly on a corrupt cert")
	}
}

func TestIssueAgentCert_ChainValidatesAndHasUniqueSerial(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	certPEM1, keyPEM1, serial1, err := IssueAgentCert(ca, "agent-1")
	if err != nil {
		t.Fatalf("IssueAgentCert (agent-1): %v", err)
	}
	_, _, serial2, err := IssueAgentCert(ca, "agent-2")
	if err != nil {
		t.Fatalf("IssueAgentCert (agent-2): %v", err)
	}
	if serial1 == serial2 {
		t.Fatal("expected distinct agent certs to have distinct serial numbers")
	}
	if serial1 == "" || serial2 == "" {
		t.Fatal("expected non-empty serial numbers")
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM)
	block, _ := pem.Decode(certPEM1)
	if block == nil {
		t.Fatal("expected a PEM block in the agent cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse agent cert: %v", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		Roots:     pool,
	}); err != nil {
		t.Fatalf("agent cert did not verify against the CA: %v", err)
	}
	if cert.Subject.CommonName != "agent-1" {
		t.Fatalf("expected CommonName agent-1, got %q", cert.Subject.CommonName)
	}
	if _, err := tls.X509KeyPair(certPEM1, keyPEM1); err != nil {
		t.Fatalf("agent cert/key did not form a valid TLS key pair: %v", err)
	}
}
