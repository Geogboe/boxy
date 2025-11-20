# Agent Registration with Tokens

## Overview

Agents register with the server using a one-time registration token. This is simpler and more secure than manual certificate distribution.

## Registration Flow

```
┌──────────────────────────────────────────────────────────────┐
│ Step 1: Admin generates registration token (on server)      │
└──────────────────────────────────────────────────────────────┘

Server Admin:
$ boxy admin generate-agent \
    --providers hyperv \
    --max-resources 50 \
    --expires 24h

Output:
┌─────────────────────────────────────────────────┐
│ Registration Token Generated                    │
├─────────────────────────────────────────────────┤
│ Token:     reg_abc123xyz789def456ghi            │
│ Expires:   2025-11-21 10:00:00 UTC (24 hours)   │
│ Providers: hyperv                               │
│ Max Res:   50                                   │
│                                                 │
│ To register agent, run on agent host:          │
│   boxy agent install \                          │
│     --server https://boxy-server:8443 \         │
│     --ca /path/to/ca-cert.pem \                 │
│     --token reg_abc123xyz789def456ghi           │
└─────────────────────────────────────────────────┘

Save CA certificate:
$ boxy admin export-ca --output ca-cert.pem


┌──────────────────────────────────────────────────────────────┐
│ Step 2: Install agent (on agent host - one time)            │
└──────────────────────────────────────────────────────────────┘

Agent Host (e.g., Windows Server):
# Copy ca-cert.pem to agent host (secure transfer)

$ boxy agent install \
    --server https://boxy-server.internal:8443 \
    --ca ca-cert.pem \
    --token reg_abc123xyz789def456ghi

Agent performs:
1. Load CA certificate (verify server identity)
2. Connect to server via TLS (server auth only)
3. Send registration request with token
4. Server validates token:
   - Not expired
   - Not already used
   - Providers allowed
5. Server generates client certificate for agent
6. Server returns certificate to agent
7. Agent stores certificate securely
8. Agent installed as system service

Output:
✓ Connected to server: boxy-server.internal:8443
✓ Token validated
✓ Agent registered as: agent-abc123def
✓ Certificate stored: /var/lib/boxy/agent/cert.pem
✓ Agent installed as service
✓ Agent started

To check status: boxy agent status
To view logs:    journalctl -u boxy-agent -f


┌──────────────────────────────────────────────────────────────┐
│ Step 3: All future communication uses mTLS                   │
└──────────────────────────────────────────────────────────────┘

Agent → Server (mTLS):
- Client cert: /var/lib/boxy/agent/cert.pem
- CA cert:     /var/lib/boxy/agent/ca.pem
- Server URL:  https://boxy-server.internal:8443

Server validates:
- Certificate signed by CA
- Certificate not expired
- Certificate not revoked
- Agent ID from cert CN
```

## Commands

### Server Admin Commands

```bash
# Initialize CA (one-time setup)
boxy admin init-ca --output /etc/boxy/ca

# Export CA certificate for distribution
boxy admin export-ca --output ca-cert.pem

# Generate agent registration token
boxy admin generate-agent \
  --providers hyperv \
  --max-resources 50 \
  --expires 24h

# List pending tokens
boxy admin list-tokens

# Revoke unused token
boxy admin revoke-token <token>

# List registered agents
boxy admin list-agents

# Revoke agent (certificate revocation)
boxy admin revoke-agent --agent-id agent-abc123

# Generate new CRL (Certificate Revocation List)
boxy admin generate-crl
```

### Agent Commands

```bash
# Install and register agent (one-time)
boxy agent install \
  --server https://boxy-server:8443 \
  --ca ca-cert.pem \
  --token <registration-token>

# Start agent (if installed as service)
boxy agent start

# Stop agent
boxy agent stop

# Restart agent
boxy agent restart

# Run agent in foreground (for debugging)
boxy agent run

# Check agent status
boxy agent status

# View agent configuration
boxy agent config

# Uninstall agent
boxy agent uninstall

# Test connection to server
boxy agent test-connection
```

## Token Management

### Token Structure

```go
type RegistrationToken struct {
    Token            string    // reg_<random 32 bytes hex>
    CreatedAt        time.Time
    ExpiresAt        time.Time
    AllowedProviders []string    // ["hyperv"] or ["docker", "kvm"]
    MaxResources     int         // Resource quota
    Used             bool        // One-time use flag
    UsedBy           string      // Agent ID that used this token
    UsedAt           *time.Time  // When token was used
    CreatedBy        string      // Admin user who created token
}
```

### Token Generation

```go
func GenerateRegistrationToken(
    providers []string,
    maxResources int,
    validFor time.Duration,
) (*RegistrationToken, error) {
    // Generate cryptographically secure random token
    tokenBytes := make([]byte, 32)
    if _, err := rand.Read(tokenBytes); err != nil {
        return nil, err
    }

    token := &RegistrationToken{
        Token:            "reg_" + hex.EncodeToString(tokenBytes),
        CreatedAt:        time.Now(),
        ExpiresAt:        time.Now().Add(validFor),
        AllowedProviders: providers,
        MaxResources:     maxResources,
        Used:             false,
    }

    // Store in database
    if err := db.CreateToken(token); err != nil {
        return nil, err
    }

    return token, nil
}
```

### Token Validation (Server-Side)

```go
func (s *Server) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
    // Validate token
    token, err := s.db.GetToken(req.Token)
    if err != nil {
        return nil, status.Error(codes.Unauthenticated, "invalid token")
    }

    // Check expiration
    if time.Now().After(token.ExpiresAt) {
        return nil, status.Error(codes.Unauthenticated, "token expired")
    }

    // Check if already used
    if token.Used {
        return nil, status.Error(codes.Unauthenticated, "token already used")
    }

    // Validate requested providers
    for _, provider := range req.Providers {
        if !contains(token.AllowedProviders, provider) {
            return nil, status.Errorf(
                codes.PermissionDenied,
                "provider %s not allowed by token", provider,
            )
        }
    }

    // Generate agent ID
    agentID := "agent-" + generateRandomID()

    // Generate client certificate for agent
    cert, key, err := s.ca.IssueCertificate(agentID, token.AllowedProviders)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to issue certificate: %v", err)
    }

    // Mark token as used
    token.Used = true
    token.UsedBy = agentID
    now := time.Now()
    token.UsedAt = &now
    s.db.UpdateToken(token)

    // Store agent info
    agent := &Agent{
        ID:              agentID,
        RegisteredAt:    time.Now(),
        Providers:       token.AllowedProviders,
        MaxResources:    token.MaxResources,
        CertExpiration:  cert.NotAfter,
        Status:          AgentStatusActive,
    }
    s.db.CreateAgent(agent)

    // Return certificate to agent
    return &pb.RegisterResponse{
        Success:      true,
        AgentId:      agentID,
        Certificate:  cert.Raw,
        PrivateKey:   key,
        CaCert:       s.ca.Certificate().Raw,
    }, nil
}
```

## Certificate Storage

### Linux (Secure Storage)

```go
func storeAgentCertificate(cert, key, ca []byte) error {
    // Create agent config directory
    configDir := "/var/lib/boxy/agent"
    if err := os.MkdirAll(configDir, 0700); err != nil {
        return err
    }

    // Write certificate (readable by agent service only)
    certPath := filepath.Join(configDir, "cert.pem")
    if err := os.WriteFile(certPath, cert, 0600); err != nil {
        return err
    }

    // Write private key (readable by agent service only)
    keyPath := filepath.Join(configDir, "key.pem")
    if err := os.WriteFile(keyPath, key, 0600); err != nil {
        return err
    }

    // Write CA certificate (readable by all)
    caPath := filepath.Join(configDir, "ca.pem")
    if err := os.WriteFile(caPath, ca, 0644); err != nil {
        return err
    }

    // Set ownership to boxy-agent user (if exists)
    setOwnership(configDir, "boxy-agent", "boxy-agent")

    return nil
}
```

### Windows (DPAPI Encryption)

```go
func storeAgentCertificate(cert, key, ca []byte) error {
    // Create agent config directory
    configDir := filepath.Join(os.Getenv("ProgramData"), "Boxy", "Agent")
    if err := os.MkdirAll(configDir, 0700); err != nil {
        return err
    }

    // Encrypt private key with DPAPI (machine scope)
    encryptedKey, err := dpapi.EncryptBytes(key, "Boxy Agent Certificate")
    if err != nil {
        return err
    }

    // Write encrypted key
    keyPath := filepath.Join(configDir, "key.dat")
    if err := os.WriteFile(keyPath, encryptedKey, 0600); err != nil {
        return err
    }

    // Certificate and CA don't need encryption (public info)
    certPath := filepath.Join(configDir, "cert.pem")
    if err := os.WriteFile(certPath, cert, 0644); err != nil {
        return err
    }

    caPath := filepath.Join(configDir, "ca.pem")
    if err := os.WriteFile(caPath, ca, 0644); err != nil {
        return err
    }

    // Set ACLs (only SYSTEM and Administrators)
    setWindowsACL(configDir)

    return nil
}
```

### macOS (Keychain)

```go
func storeAgentCertificate(cert, key, ca []byte) error {
    // Import certificate and key into system keychain
    // This provides hardware-backed encryption on Macs with T2/M1+

    keychain := "/Library/Keychains/System.keychain"

    // Create temporary PKCS12 bundle
    p12, err := createPKCS12(cert, key, "Boxy Agent")
    if err != nil {
        return err
    }
    defer os.Remove(p12)

    // Import into keychain
    cmd := exec.Command("security", "import", p12, "-k", keychain, "-T", "/usr/local/bin/boxy")
    if err := cmd.Run(); err != nil {
        return err
    }

    // Store CA separately (doesn't need keychain)
    configDir := "/Library/Application Support/Boxy/Agent"
    os.MkdirAll(configDir, 0755)

    caPath := filepath.Join(configDir, "ca.pem")
    return os.WriteFile(caPath, ca, 0644)
}
```

## Agent Configuration File

After installation, agent config stored at:
- Linux: `/etc/boxy/agent.yaml`
- Windows: `C:\ProgramData\Boxy\agent.yaml`
- macOS: `/Library/Application Support/Boxy/agent.yaml`

```yaml
# agent.yaml (generated during install)
agent:
  id: agent-abc123def
  server_url: https://boxy-server.internal:8443

  # Certificate paths
  tls:
    cert_file: /var/lib/boxy/agent/cert.pem
    key_file: /var/lib/boxy/agent/key.pem
    ca_file: /var/lib/boxy/agent/ca.pem

  # Providers to expose
  providers:
    - hyperv

  # Heartbeat interval
  heartbeat_interval: 30s

  # Listen address for incoming provider requests
  listen_address: 0.0.0.0:8444

  # Logging
  log_level: info
  log_file: /var/log/boxy-agent.log
```

## Service Installation

### Linux (systemd)

```bash
# Install creates systemd unit file
cat > /etc/systemd/system/boxy-agent.service <<EOF
[Unit]
Description=Boxy Agent
After=network.target

[Service]
Type=simple
User=boxy-agent
Group=boxy-agent
ExecStart=/usr/local/bin/boxy agent run
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

# Security hardening
PrivateTmp=true
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/boxy

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable boxy-agent
systemctl start boxy-agent
```

### Windows (Service)

```powershell
# Install creates Windows service
sc.exe create BoxyAgent `
  binPath= "C:\Program Files\Boxy\boxy.exe agent run" `
  start= auto `
  DisplayName= "Boxy Agent"

sc.exe description BoxyAgent "Boxy remote provider agent"
sc.exe start BoxyAgent
```

### macOS (launchd)

```bash
# Install creates launchd plist
cat > /Library/LaunchDaemons/com.boxy.agent.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.boxy.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/boxy</string>
        <string>agent</string>
        <string>run</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/boxy-agent.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/boxy-agent.log</string>
</dict>
</plist>
EOF

launchctl load /Library/LaunchDaemons/com.boxy.agent.plist
```

## Security Considerations

### Token Security

**Best Practices**:
- Generate tokens with high entropy (32+ bytes random)
- Short expiration (24 hours recommended)
- One-time use only
- Secure transfer (encrypted channel, not email)
- Revoke unused tokens after use

### Certificate Security

**Best Practices**:
- Store private keys with restricted permissions (0600)
- Use OS-specific secure storage when available (DPAPI, Keychain)
- Short-lived certificates (90 days)
- Automated renewal before expiration
- Certificate revocation support

### Network Security

**Best Practices**:
- Require server TLS (verify CA)
- Require client certificates after registration
- Strong cipher suites only (TLS 1.2+)
- Network firewall rules (allow only agent→server)

## Troubleshooting

### Agent Can't Connect to Server

```bash
# Test network connectivity
curl -v https://boxy-server:8443

# Test TLS with CA cert
openssl s_client -connect boxy-server:8443 -CAfile ca-cert.pem

# Check agent logs
journalctl -u boxy-agent -n 100
```

### Token Invalid or Expired

```bash
# Check token status (server-side)
boxy admin list-tokens

# Generate new token
boxy admin generate-agent --providers hyperv --expires 24h
```

### Certificate Issues

```bash
# Verify certificate not expired
openssl x509 -in /var/lib/boxy/agent/cert.pem -noout -dates

# Verify certificate signed by CA
openssl verify -CAfile /var/lib/boxy/agent/ca.pem /var/lib/boxy/agent/cert.pem

# Check CRL
boxy admin generate-crl
curl https://boxy-server:8443/crl.pem -o crl.pem
openssl crl -in crl.pem -noout -text
```

## Summary

Token-based registration provides:
- ✅ **Simple setup**: Copy CA cert + token, run install
- ✅ **Secure**: One-time tokens, mTLS after registration
- ✅ **Automated**: Certificate generation handled by server
- ✅ **Auditable**: Track which tokens used by which agents
- ✅ **Revocable**: Revoke agents via certificate revocation

This is much easier than manual certificate distribution while maintaining strong security.
