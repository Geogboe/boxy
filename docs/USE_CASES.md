# Boxy Use Cases

This document describes the primary and secondary use cases that drive Boxy's design decisions.

---

## Primary Use Case: Quick Testing Environment

**Problem:**

Developers and testers need instant, clean, isolated environments to test software, configurations, or scripts without affecting their main system or other projects.

**User Story:**

> "I need to quickly test this installer on a clean Windows 11 machine, but I don't want to spend 10 minutes provisioning a VM or worry about cleanup afterward."

**Boxy Solution:**

1. User requests a sandbox: `boxy sandbox create -p win11-test:1 -d 1h`
2. If preheated resource available → **instant allocation** (< 5 seconds)
3. If only cold resource available → start VM → allocate (30-60 seconds)
4. User gets connection info (RDP/SSH) with auto-generated credentials
5. Test software in clean environment
6. Sandbox auto-expires after 1 hour (or user destroys manually)
7. VM is destroyed (never reused - always clean)
8. Pool automatically replenishes with new VM

**Example Configuration:**

```yaml
pools:
  - name: win11-test
    type: vm
    backend: hyperv
    image: windows-11-base.vhdx

    # Keep 10 total VMs, 3 running and ready
    min_ready: 10
    max_total: 20

    preheating:
      enabled: true
      count: 3              # Keep 3 VMs warm/running
      recycle_interval: 1h  # Recycle every hour

    cpus: 4
    memory_mb: 8192

    hooks:
      on_provision:
        - type: script
          shell: powershell
          inline: |
            # Validate VM is accessible
            Test-Connection localhost -Count 1

      on_allocate:
        - type: script
          shell: powershell
          inline: |
            # Create user account with random password
            New-LocalUser -Name "${username}" -Password (ConvertTo-SecureString "${password}" -AsPlainText -Force)
            Add-LocalGroupMember -Group "Administrators" -Member "${username}"
```

**Key Benefits:**

- ✅ **Speed**: Preheated VMs available instantly
- ✅ **Cleanliness**: Always fresh, no state drift
- ✅ **Simplicity**: One command to get working environment
- ✅ **Auto-cleanup**: No orphaned resources

**Similar to:**

- Windows Sandbox feature
- Docker containers for testing
- Disposable VMs in cloud providers

---

## Secondary Use Cases

### UC2: CI/CD Ephemeral Runners

**Problem:**

CI/CD pipelines need fresh, isolated environments for each build to prevent test contamination and ensure reproducibility.

**User Story:**

> "Our Jenkins builds sometimes fail due to leftover state from previous builds. We need truly ephemeral build agents."

**Boxy Solution:**

```yaml
# CI pipeline script
sandbox_id=$(boxy sandbox create -p ci-runner:1 -d 30m --json | jq -r '.id')
# Run tests in sandbox
boxy sandbox destroy $sandbox_id
```

**Configuration:**

```yaml
pools:
  - name: ci-runner
    type: container
    backend: docker
    image: ubuntu:22.04

    min_ready: 5      # Always keep 5 ready
    max_total: 20     # Scale up during busy times

    preheating:
      enabled: true
      count: 5        # All resources preheated

    hooks:
      on_provision:
        - type: script
          shell: bash
          inline: |
            apt-get update
            apt-get install -y git build-essential

      on_allocate:
        - type: script
          shell: bash
          inline: |
            # Setup CI environment variables
            echo "CI=true" >> /etc/environment
```

**Key Benefits:**

- ✅ **Isolation**: Each build in fresh environment
- ✅ **Speed**: Preheated containers ready instantly
- ✅ **Scalability**: Auto-replenish during high load
- ✅ **Cost-effective**: Destroy immediately after build

---

### UC3: Security Red Teaming

**Problem:**

Security researchers need isolated, disposable environments for malware analysis, exploit testing, and attack simulation.

**User Story:**

> "I need to detonate malware samples in isolated VMs without contaminating my lab infrastructure."

**Boxy Solution:**

```bash
# Create isolated malware analysis lab
boxy sandbox create \
  -p malware-analysis:1 \
  -p victim-windows:2 \
  -d 4h \
  -n red-team-lab-001
```

**Configuration:**

```yaml
pools:
  - name: malware-analysis
    type: vm
    backend: hyperv
    image: windows-server-2022-analysis.vhdx

    min_ready: 3
    max_total: 10

    preheating:
      enabled: true
      count: 2
      recycle_interval: 30m  # Frequent recycling for security

    hooks:
      on_provision:
        - type: script
          shell: powershell
          inline: |
            # Install analysis tools
            choco install procmon sysinternals

      on_allocate:
        - type: script
          shell: powershell
          inline: |
            # Create unique hostname
            Rename-Computer -NewName "MALWARE-${RESOURCE_ID}" -Force
```

**Key Benefits:**

- ✅ **Security**: Complete isolation per analysis session
- ✅ **Cleanliness**: Always fresh VMs, no contamination
- ✅ **Recycling**: Automatic refresh prevents persistence
- ✅ **Snapshots**: Easy to restore to known-good state (via differencing disks)

**Future Enhancement (v2):**

- Network isolation between sandboxes
- Overlay networks (WireGuard/Headscale)
- Snapshot/restore capabilities via hooks

---

### UC4: Development Environments

**Problem:**

Developers need quick, reproducible development environments for feature branches or experimentation.

**User Story:**

> "I want to quickly spin up a full dev stack (database + web server + cache) to test my feature branch."

**Boxy Solution:**

```bash
# Multi-resource sandbox
boxy sandbox create \
  -p postgres-db:1 \
  -p nginx-web:1 \
  -p redis-cache:1 \
  -d 8h \
  -n feature-xyz-dev
```

**Configuration:**

```yaml
pools:
  - name: postgres-db
    type: container
    backend: docker
    image: postgres:15

    min_ready: 3
    preheating:
      enabled: true
      count: 3

    environment:
      POSTGRES_PASSWORD: "${password}"  # Auto-generated

    hooks:
      on_provision:
        - type: script
          shell: bash
          inline: |
            # Wait for postgres to be ready
            until pg_isready; do sleep 1; done

  - name: nginx-web
    type: container
    backend: docker
    image: nginx:latest

    min_ready: 3
    preheating:
      enabled: true
      count: 3

  - name: redis-cache
    type: container
    backend: docker
    image: redis:7

    min_ready: 2
    preheating:
      enabled: true
      count: 2
```

**Key Benefits:**

- ✅ **Speed**: Entire stack ready in seconds
- ✅ **Isolation**: Feature branches don't interfere
- ✅ **Reproducibility**: Same environment every time
- ✅ **Auto-cleanup**: Destroy when feature merged

---

### UC5: Training & Education

**Problem:**

Instructors need to provision identical lab environments for students in workshops or training sessions.

**User Story:**

> "I'm teaching a Docker workshop with 30 students. Each needs their own Ubuntu VM with Docker installed."

**Boxy Solution:**

```bash
# Provision 30 sandboxes
for i in {1..30}; do
  boxy sandbox create -p docker-training:1 -d 4h -n student-$i
done
```

**Configuration:**

```yaml
pools:
  - name: docker-training
    type: vm
    backend: hyperv
    image: ubuntu-22.04-docker.vhdx

    min_ready: 30     # Pre-provision for entire class
    max_total: 40     # Buffer for late students

    preheating:
      enabled: true
      count: 30       # All preheated for instant access

    cpus: 2
    memory_mb: 4096

    hooks:
      on_allocate:
        - type: script
          shell: bash
          inline: |
            # Create student user
            useradd -m -s /bin/bash ${username}
            echo "${username}:${password}" | chpasswd
            usermod -aG docker ${username}
```

**Key Benefits:**

- ✅ **Scale**: Provision many environments quickly
- ✅ **Consistency**: Every student has identical setup
- ✅ **Simplicity**: One command per student
- ✅ **Cost control**: Auto-destroy after workshop

---

### UC6: Compliance & Audit Testing

**Problem:**

Organizations need to test compliance controls in isolated environments without affecting production.

**User Story:**

> "We need to validate our security controls meet SOC2 requirements in a test environment."

**Boxy Solution:**

```bash
boxy sandbox create \
  -p compliance-test-env:3 \
  -d 24h \
  -n soc2-audit-2024
```

**Configuration:**

```yaml
pools:
  - name: compliance-test-env
    type: vm
    backend: hyperv
    image: windows-server-2022-hardened.vhdx

    min_ready: 5
    max_total: 10

    preheating:
      enabled: false  # Compliance testing is scheduled, not ad-hoc

    hooks:
      on_provision:
        - type: script
          shell: powershell
          inline: |
            # Validate hardening
            Get-Service | Where-Object {$_.Name -eq "SecurityAudit"}

      on_allocate:
        - type: script
          shell: powershell
          inline: |
            # Enable audit logging
            auditpol /set /category:"Logon/Logoff" /success:enable /failure:enable
```

**Key Benefits:**

- ✅ **Isolation**: Test without production impact
- ✅ **Reproducibility**: Same environment for each audit
- ✅ **Documentation**: Track which sandbox used for which audit
- ✅ **Cleanup**: Destroy after audit complete

---

## Use Case Comparison Matrix

| Use Case | Primary Resource Type | Preheating | Typical Duration | Volume |
| ---------- | ---------------------- | ------------ | ------------------ | -------- |
| Quick Testing | VMs | High (3-5) | 1-2 hours | Low |
| CI/CD Runners | Containers | High (5-10) | 10-30 minutes | High |
| Red Teaming | VMs | Medium (2-3) | 4-8 hours | Low |
| Development | Containers | High (3-5) | 4-8 hours | Medium |
| Training | VMs | Very High (30+) | 2-4 hours | Burst |
| Compliance | VMs | Low (0-2) | 8-24 hours | Low |

---

## Anti-Patterns (What Boxy is NOT For)

### ❌ Long-Running Production Services

**Why not:** Boxy is designed for ephemeral, disposable environments. Production services should use dedicated infrastructure.

**Use instead:** Docker Swarm, Kubernetes, traditional VMs

### ❌ Stateful Databases (Production)

**Why not:** Resources are destroyed on release. No persistence guarantees.

**Use instead:** Managed database services, dedicated DB servers

### ❌ Cost-Insensitive Workloads

**Why not:** Preheating keeps resources running, which costs money. Only use for workloads where speed justifies cost.

**Use instead:** On-demand provisioning, serverless functions

### ❌ Multi-Month Environments

**Why not:** Boxy optimizes for quick allocation and cleanup. Long-running environments waste preheated resources.

**Use instead:** Traditional VMs, persistent infrastructure

---

## Design Principles Derived from Use Cases

Based on the primary use case (Quick Testing Environment), Boxy's design prioritizes:

1. **Speed over cost** - Preheating resources costs money but saves time
2. **Cleanliness over efficiency** - Never reuse resources, always provision fresh
3. **Simplicity over flexibility** - One command to get working environment
4. **Automation over control** - Auto-cleanup, auto-replenishment, minimal user intervention

These principles may not fit all use cases, which is why certain scenarios (production databases, long-running services) are explicitly anti-patterns.

---

## Future Use Cases (v2+)

- **Multi-resource networking**: Sandboxes with VPN/overlay networks
- **Persistent storage**: Attach volumes that survive resource destruction
- **GPU workloads**: ML training, rendering farms
- **Multi-cloud**: Resources across AWS, Azure, GCP
- **Scheduled provisioning**: "I need 50 VMs ready by 9am tomorrow"

---

**Last Updated:** 2024-11-22
**Version:** v1 (v1-prerelease)
