For an overview of the entire system architecture, please refer to the [Boxy v1 Complete Architecture Map](../ARCHITECTURE_MAP.md).

# Security Guide for Distributed Boxy Architecture

## Table of Contents

1. [Threat Model](#threat-model)
2. [Certificate Management](#certificate-management)
3. [Network Security](#network-security)
4. [Authorization Model](#authorization-model)
5. [Credential Protection](#credential-protection)
6. [Audit Logging](#audit-logging)
7. [Security Checklist](#security-checklist)

## Threat Model

### Assets to Protect

1. **Resource Credentials**: Passwords, SSH keys for provisioned resources
2. **Agent Certificates**: TLS certificates for agent authentication
3. **CA Private Key**: Root of trust for entire system
4. **Resource Data**: Contents of VMs, containers
5. **Control Plane**: Ability to provision/destroy resources

### Threat Actors

1. **External Attacker**: No privileged access, attempts network attacks
2. **Compromised Agent**: Agent host is compromised
3. **Malicious Insider**: Authenticated user attempting privilege escalation
4. **Network Eavesdropper**: Passive monitoring of network traffic

### Attack Scenarios & Mitigations

| Attack | Impact | Mitigation |
| ------ | ------ | ---------- |
| **Man-in-the-Middle** | Intercept/modify agent-server traffic | mTLS with certificate pinning |
| **Rogue Agent** | Unauthorized resource provisioning | Certificate-based auth + agent authorization |
| **Credential Theft** | Access to provisioned resources | Encryption at rest, short-lived creds |
| **CA Key Compromise** | Complete trust breakdown | HSM storage, key ceremony, rotation |
| **Agent Impersonation** | Provision resources as another agent | Client certificate verification |
| **DoS Attack** | Service unavailability | Rate limiting, connection limits, resource quotas |
| **Privilege Escalation** | Unauthorized provider access | Least privilege, provider-level authorization |
| **Replay Attack** | Reuse of captured requests | Request IDs, timestamps, nonce |

## Certificate Management

### Certificate Hierarchy

```text
┌─────────────────────────┐
│      Root CA            │
│  (10-year validity)     │
│  (offline, HSM)         │
└───────────┬─────────────┘
            │
    ┌───────┴────────┐
    │                │
┌───▼────────┐  ┌───▼────────┐
│  Server    │  │  Agent     │
│   Cert     │  │   Certs    │
│ (1 year)   │  │ (90 days)  │
└────────────┘  └────────────┘
```

### CA Initialization (One-Time)

**Ceremony**: Generate CA with proper security:

```bash
# Use dedicated, air-gapped machine for CA operations
# (In production, use HSM)

# Initialize CA
boxy admin init-ca \
  --output /secure/boxy-ca \
  --key-size 4096 \
  --validity-years 10 \
  --organization "Your Organization" \
  --country US

# Output:
#   /secure/boxy-ca/ca-cert.pem  (public, distribute)
#   /secure/boxy-ca/ca-key.pem   (SECRET, offline storage)
```

**Security Requirements**:

- [ ] Generate on offline, trusted machine
- [ ] Use strong entropy source
- [ ] Store private key on encrypted volume
- [ ] Backup private key to secure, offline location
- [ ] Document key custodians (require 2-of-3 for access)
- [ ] Set calendar reminder for renewal (9 years from now)

### Server Certificate Issuance

```bash
# Generate server certificate (1-year validity)
boxy admin issue-cert \
  --ca-cert /secure/boxy-ca/ca-cert.pem \
  --ca-key /secure/boxy-ca/ca-key.pem \
  --cert-type server \
  --common-name "boxy-server" \
  --dns-names "boxy-server.internal,boxy.example.com" \
  --ip-addresses "10.0.1.100" \
  --validity-days 365 \
  --output /etc/boxy/server

# Output:
#   /etc/boxy/server/server-cert.pem
#   /etc/boxy/server/server-key.pem
```

### Agent Certificate Issuance

```bash
# Generate agent certificate (90-day validity)
boxy admin issue-cert \
  --ca-cert /secure/boxy-ca/ca-cert.pem \
  --ca-key /secure/boxy-ca/ca-key.pem \
  --cert-type agent \
  --agent-id "windows-host-01" \
  --common-name "windows-host-01" \
  --dns-names "windows-host-01.internal" \
  --validity-days 90 \
  --output /etc/boxy/agents/windows-01

# Output:
#   /etc/boxy/agents/windows-01/agent-cert.pem
#   /etc/boxy/agents/windows-01/agent-key.pem
```

**Agent Cert Fields**:

- `Common Name (CN)`: Agent ID (used for authorization)
- `Organization (O)`: Your organization
- `Validity`: 90 days (short-lived for better security)
- `Key Usage`: Digital Signature, Key Encipherment
- `Extended Key Usage`: Client Auth, Server Auth

### Certificate Distribution

**Secure Transfer**:

```bash
# Option 1: SCP with SSH key auth (recommended)
scp /etc/boxy/agents/windows-01/* admin@windows-host-01:C:/boxy/

# Option 2: Encrypted USB drive (air-gapped)
# Copy to USB, physically deliver, verify hash

# Option 3: Secrets management system (production)
# Store in HashiCorp Vault, fetch on agent startup
```

**Verification on Agent**:

```bash
# Verify certificate is valid
openssl x509 -in agent-cert.pem -noout -text

# Verify certificate is signed by CA
openssl verify -CAfile ca-cert.pem agent-cert.pem

# Check expiration
openssl x509 -in agent-cert.pem -noout -dates
```

### Certificate Renewal

**Automated Renewal** (recommended):

```bash
# Create renewal script (run 30 days before expiration)
#!/bin/bash
# /etc/boxy/scripts/renew-cert.sh

AGENT_ID="windows-host-01"
CA_CERT="/secure/ca-cert.pem"
CA_KEY="/secure/ca-key.pem"
OUTPUT_DIR="/etc/boxy/agents/$AGENT_ID"

# Issue new certificate
boxy admin issue-cert \
  --ca-cert "$CA_CERT" \
  --ca-key "$CA_KEY" \
  --cert-type agent \
  --agent-id "$AGENT_ID" \
  --validity-days 90 \
  --output "$OUTPUT_DIR"

# Restart agent with new cert
systemctl restart boxy-agent
```

**Monitoring**:

```bash
# Prometheus metric
boxy_certificate_expiry_seconds{agent_id="windows-host-01"} 2592000

# Alert rule (alert 30 days before expiration)
alert: CertificateExpiringSoon
expr: boxy_certificate_expiry_seconds < 30 * 24 * 3600
```

### Certificate Revocation

**Create CRL (Certificate Revocation List)**:

```bash
# Revoke compromised certificate
boxy admin revoke-cert \
  --ca-cert /secure/boxy-ca/ca-cert.pem \
  --ca-key /secure/boxy-ca/ca-key.pem \
  --serial-number 0x1234567890ABCDEF \
  --reason key-compromise

# Generate CRL
boxy admin generate-crl \
  --ca-cert /secure/boxy-ca/ca-cert.pem \
  --ca-key /secure/boxy-ca/ca-key.pem \
  --output /etc/boxy/ca-crl.pem

# Distribute CRL to server
scp /etc/boxy/ca-crl.pem boxy-server:/etc/boxy/
```

**Server Configuration**:

```yaml
# boxy.yaml
server:
  tls:
    ca_file: /etc/boxy/ca-cert.pem
    crl_file: /etc/boxy/ca-crl.pem  # Enable CRL checking
    verify_client_cert: true
```

## Network Security

### TLS Configuration

**Minimum TLS Version**: TLS 1.2 (prefer TLS 1.3)

**Allowed Cipher Suites** (strong only):

```go
tlsConfig := &tls.Config{
    MinVersion: tls.VersionTLS12,
    CipherSuites: []uint16{
        tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
        tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
        tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
        tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
    },
    PreferServerCipherSuites: true,
    CurvePreferences: []tls.CurveID{
        tls.CurveP256,
        tls.X25519,
    },
}
```

**Perfect Forward Secrecy**: Use ECDHE cipher suites

### Network Isolation

**Firewall Rules**:

```bash
# Server (Linux)
# Allow agent connections on 8443
iptables -A INPUT -p tcp --dport 8443 -s 10.0.1.0/24 -j ACCEPT
iptables -A INPUT -p tcp --dport 8443 -j DROP

# Agent (Windows)
# Allow server connections on 8444
New-NetFirewallRule -DisplayName "Boxy Agent" \
  -Direction Inbound \
  -Protocol TCP \
  -LocalPort 8444 \
  -RemoteAddress 10.0.1.100 \
  -Action Allow
```

**Network Segmentation**:

- Server and agents on dedicated management VLAN
- Provisioned resources on separate VLAN
- No direct access from provisioned resources to control plane

### Rate Limiting

**Server-Side**:

```go
// Limit 100 requests per second per agent
rateLimiter := ratelimit.New(100)

interceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
    rateLimiter.Take()
    return handler(ctx, req)
}
```

**Connection Limits**:

```go
grpcServer := grpc.NewServer(
    grpc.MaxConcurrentStreams(100),
    grpc.KeepaliveParams(keepalive.ServerParameters{
        MaxConnectionIdle: 5 * time.Minute,
        MaxConnectionAge:  15 * time.Minute,
    }),
)
```

## Authorization Model

### Provider Authorization

**Agent Authorization Matrix**:

| Agent ID | Authorized Providers |
| ---------- | --------------------- |
| windows-host-01 | hyperv |
| linux-host-01 | docker, kvm |
| docker-host-02 | docker |

**Configuration**:

```yaml
# boxy.yaml (server)
agents:
  - id: windows-host-01
    providers:
      - hyperv
    max_resources: 50

  - id: linux-host-01
    providers:
      - docker
      - kvm
    max_resources: 100
```

**Enforcement**:

```go
func (s *Server) authorizeProviderAccess(agentID, providerName string) error {
    agent, ok := s.config.GetAgent(agentID)
    if !ok {
        return ErrAgentNotAuthorized
    }

    if !contains(agent.Providers, providerName) {
        return fmt.Errorf("agent %s not authorized for provider %s", agentID, providerName)
    }

    return nil
}
```

### Request Authentication

**Extract Agent ID from Certificate**:

```go
func getAgentIDFromContext(ctx context.Context) (string, error) {
    peer, ok := peer.FromContext(ctx)
    if !ok {
        return "", errors.New("no peer info")
    }

    tlsInfo, ok := peer.AuthInfo.(credentials.TLSInfo)
    if !ok {
        return "", errors.New("no TLS info")
    }

    if len(tlsInfo.State.PeerCertificates) == 0 {
        return "", errors.New("no client certificate")
    }

    cert := tlsInfo.State.PeerCertificates[0]
    agentID := cert.Subject.CommonName

    return agentID, nil
}
```

### Resource Quotas

**Per-Agent Limits**:

```go
type AgentQuota struct {
    MaxResources      int
    MaxCPU            int
    MaxMemoryMB       int
    MaxDiskGB         int
    CurrentResources  int
    CurrentCPU        int
    CurrentMemoryMB   int
}

func (s *Server) checkQuota(agentID string, spec ResourceSpec) error {
    quota := s.quotaManager.GetQuota(agentID)

    if quota.CurrentResources+1 > quota.MaxResources {
        return ErrQuotaExceeded
    }

    if quota.CurrentCPU+spec.CPUs > quota.MaxCPU {
        return ErrQuotaExceeded
    }

    // Check other limits...

    return nil
}
```

## Credential Protection

### Encryption at Rest

**Resource Credentials**:

- Stored encrypted in database
- Encryption key derived from server's master key
- Master key stored in environment variable or secure vault

```go
// Encrypt password before storing in DB
encryptedPassword, err := encryptor.Encrypt(password)
resource.Metadata["password_encrypted"] = encryptedPassword

// Decrypt when returning connection info
password, err := encryptor.Decrypt(encryptedPassword)
connectionInfo.Password = password
```

### Encryption in Transit

- All credentials transmitted over mTLS
- TLS 1.2+ with strong cipher suites
- Perfect forward secrecy (ECDHE)

### Credential Rotation

**Auto-Rotation**:

```go
// Rotate credentials after 24 hours
func (s *Service) rotateResourceCredentials(ctx context.Context, resourceID string) error {
    res, err := s.repo.GetResource(ctx, resourceID)
    if err != nil {
        return err
    }

    // Generate new password
    newPassword := generatePassword(16)

    // Update resource (provider-specific)
    if err := s.provider.UpdatePassword(ctx, res, newPassword); err != nil {
        return err
    }

    // Update database
    encryptedPassword, _ := s.encryptor.Encrypt(newPassword)
    res.Metadata["password_encrypted"] = encryptedPassword
    return s.repo.UpdateResource(ctx, res)
}
```

### No Credential Logging

**Redact Sensitive Fields**:

```go
func (ci *ConnectionInfo) Redacted() *ConnectionInfo {
    redacted := *ci
    if redacted.Password != "" {
        redacted.Password = "***REDACTED***"
    }
    if redacted.SSHKey != "" {
        redacted.SSHKey = "***REDACTED***"
    }
    return &redacted
}

// In logs
logger.WithField("connection_info", connInfo.Redacted()).Info("Resource ready")
```

## Audit Logging

### What to Log

**Authentication Events**:

- Agent registration attempts (success/failure)
- Certificate validation failures
- Agent disconnections

**Authorization Events**:

- Provider access attempts (allowed/denied)
- Quota violations
- Unauthorized requests

**Resource Operations**:

- Provision requests (who, what, when, result)
- Destroy operations
- Status checks
- Connection info access

### Log Format

**Structured Logging** (JSON):

```json
{
  "timestamp": "2025-11-20T10:30:00Z",
  "level": "info",
  "event": "provision",
  "agent_id": "windows-host-01",
  "provider": "hyperv",
  "resource_id": "res-abc123",
  "pool_id": "win-server-2022",
  "sandbox_id": "sb-xyz789",
  "result": "success",
  "duration_ms": 1523,
  "request_id": "req-uuid-1234"
}
```

### Audit Retention

- **Security events**: 1 year minimum
- **Resource operations**: 90 days minimum
- **Debug logs**: 7 days

### Monitoring & Alerting

**Critical Alerts**:

```yaml
# Prometheus alert rules
groups:
  - name: security
    rules:
      - alert: FailedAuthenticationSpike
        expr: rate(boxy_auth_failures_total[5m]) > 10
        annotations:
          summary: "High rate of authentication failures"

      - alert: UnauthorizedProviderAccess
        expr: increase(boxy_authz_denials_total[5m]) > 5
        annotations:
          summary: "Multiple authorization denials detected"

      - alert: CertificateExpiringSoon
        expr: boxy_cert_expiry_seconds < 30 * 24 * 3600
        annotations:
          summary: "Certificate expiring in less than 30 days"
```

## Security Checklist

### Pre-Production

- [ ] CA private key stored securely (HSM or encrypted offline storage)
- [ ] CA private key backed up to secure location
- [ ] Server certificate issued with correct SANs
- [ ] Agent certificates issued for all agents
- [ ] TLS 1.2+ enforced on server and agents
- [ ] Strong cipher suites configured
- [ ] Client certificate verification enabled
- [ ] CRL distribution configured
- [ ] Firewall rules restrict access to management ports
- [ ] Network segmentation between control plane and workloads
- [ ] Rate limiting configured
- [ ] Resource quotas configured per agent
- [ ] Credential encryption at rest enabled
- [ ] Master encryption key secured (env var or vault)
- [ ] Audit logging enabled
- [ ] Log retention policy configured
- [ ] Security alerts configured
- [ ] Incident response plan documented

### Ongoing Operations

- [ ] Monitor certificate expiration (alert 30 days before)
- [ ] Renew certificates before expiration
- [ ] Review audit logs weekly
- [ ] Review agent authorization matrix monthly
- [ ] Test certificate revocation quarterly
- [ ] Update CRL after revocations
- [ ] Patch Boxy to latest version monthly
- [ ] Review firewall rules quarterly
- [ ] Conduct security audit annually
- [ ] Test disaster recovery procedures quarterly

### Incident Response

**Compromised Agent**:

1. Revoke agent certificate immediately
2. Update and distribute CRL
3. Investigate what resources were accessed
4. Rotate credentials for affected resources
5. Review audit logs for suspicious activity
6. Issue new certificate with different ID after remediation

**CA Key Compromise**:

1. **CRITICAL**: Generate new CA immediately
2. Issue new certificates for all servers and agents
3. Distribute new CA cert to all components
4. Revoke old CA
5. Investigate scope of compromise
6. Notify stakeholders

**Credential Leak**:

1. Rotate affected credentials immediately
2. Destroy and re-provision affected resources
3. Review audit logs for unauthorized access
4. Update access controls if needed

## Best Practices Summary

1. **Defense in Depth**: Multiple layers of security (mTLS + authz + quotas + audit)
2. **Least Privilege**: Agents only authorized for specific providers
3. **Short-Lived Credentials**: Certificates expire frequently (90 days for agents)
4. **Secure Defaults**: Require client auth, strong ciphers, TLS 1.2+
5. **Audit Everything**: Log all security-relevant events
6. **Monitor Actively**: Alert on anomalies and failures
7. **Plan for Compromise**: Have incident response procedures
8. **Automate Renewals**: Prevent expiration incidents
9. **Test Regularly**: Verify security controls work
10. **Document Thoroughly**: Ensure knowledge transfer

## References

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [CIS Benchmarks](https://www.cisecurity.org/cis-benchmarks/)
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework)
- [gRPC Security Best Practices](https://grpc.io/docs/guides/auth/)
- [TLS Best Practices](https://wiki.mozilla.org/Security/Server_Side_TLS)