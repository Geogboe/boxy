package pki

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
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
		t.Fatal("expected a second IssueServerCert call to reload the existing cert, not mint a new one")
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
