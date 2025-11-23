# 01: VSCode Extension

---

## Metadata

```yaml
feature: "VSCode Extension"
slug: "vscode-extension"
status: "not-started"
priority: "high"
type: "feature"
effort: "large"
depends_on: ["v1-rest-api", "v1-multi-tenancy"]
enables: ["developer-experience", "ide-integration"]
testing: ["manual", "e2e"]
breaking_change: false
week: "1-3"
```

---

## Overview

VSCode extension for managing Boxy sandboxes directly from IDE:
- Create/destroy sandboxes from command palette
- Tree view showing active sandboxes
- SSH/RDP connection with one click
- Pool health visibility
- Real-time sandbox status

**Tech Stack**: TypeScript, VSCode Extension API, REST client

---

## Features

### Sidebar Tree View
```
BOXY EXTENSION
├── 📦 Sandboxes
│   ├── 🟢 my-dev-env (Ready)
│   │   ├── 📊 Resources: 3
│   │   ├── ⏰ Expires: 2h 15m
│   │   ├── 🔗 Connect via SSH
│   │   └── 🗑️ Destroy
│   └── 🟡 test-lab (Creating)
├── 🏊 Pools
│   ├── win-server-2022 (Ready: 3, Allocated: 2)
│   └── ubuntu-containers (Ready: 5, Allocated: 1)
└── ⚙️ Settings
```

### Commands
- `Boxy: Create Sandbox`
- `Boxy: Destroy Sandbox`
- `Boxy: Connect to Sandbox`
- `Boxy: Extend Duration`
- `Boxy: View Pool Stats`

---

## Configuration

```json
{
  "boxy.serverUrl": "https://boxy-server:8443",
  "boxy.apiToken": "bxy_...",
  "boxy.refreshInterval": 30
}
```

---

## Success Criteria

- ✅ Extension published to VSCode marketplace
- ✅ Sandbox CRUD works
- ✅ Tree view updates in real-time
- ✅ SSH/RDP connection works
- ✅ Pool stats visible
- ✅ Error handling and feedback

---

**Last Updated**: 2025-11-23
**Implementation Status**: Not Started
**Blocked By**: v1-rest-api, v1-multi-tenancy
