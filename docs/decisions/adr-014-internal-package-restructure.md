# ADR-014: Internal Package Restructure

**Date**: 2025-11-24
**Status**: Accepted

## Context

The original `internal/` package organization had grown organically and lacked a clear narrative:

```
internal/
├── core/           # Domain logic
│   ├── sandbox/
│   ├── pool/
│   └── allocator/
├── server/         # ??? (felt like a dumping ground)
├── hooks/          # ??? (unclear where it belonged)
├── api/http/
├── storage/
├── config/
└── bootstrap/providers/
```

Problems identified:
1. **`server/` lacked clear purpose** - Mixed runtime orchestration, initialization helpers, agent bootstrap, and sandbox CLI helpers in one package
2. **`hooks/` placement unclear** - Not obviously infrastructure or domain concern
3. **Missing narrative** - Hard to understand the relationship between packages and their dependencies
4. **`bootstrap/providers/` too nested** - Only contained provider registry construction

We wanted the structure to tell a story: "What does boxy do?" → "How is it wired together?" → "How does it run?"

## Decision

We restructured `internal/` to follow a clear layered narrative:

### New Structure

```
internal/
├── core/                    # DOMAIN LOGIC: What boxy does
│   ├── sandbox/            # Sandbox lifecycle management
│   ├── pool/               # Resource pool management
│   ├── allocator/          # Resource allocation orchestration
│   └── lifecycle/          # NEW: Lifecycle concerns
│       └── hooks/          # Hook execution (moved from internal/hooks/)
│
├── runtime/                 # APPLICATION: How it's wired and run
│   ├── runtime.go          # Main Runtime orchestration (was server/service.go)
│   ├── providers.go        # Provider registry construction (was server/providers.go + bootstrap/providers/)
│   ├── agents.go           # Remote agent setup (was server/remote_agents.go + agent_bootstrap.go)
│   ├── storage.go          # Storage init helpers (was server/storage.go)
│   └── sandbox.go          # Sandbox CLI helpers (was server/sandbox_runtime.go)
│
├── api/                     # INTERFACES: External communication
│   └── http/               # REST API
│
├── storage/                 # INFRASTRUCTURE: Persistence
├── config/                  # INFRASTRUCTURE: Configuration
└── (removed bootstrap/)     # Flattened into runtime/

pkg/
└── client/                  # NEW: Public Go SDK (stubbed)
```

### Key Changes

1. **`hooks/` → `core/lifecycle/hooks/`**
   - Hooks are domain logic - they define *when* things happen in a resource's lifecycle
   - Part of business rules, not infrastructure
   - Clear naming: "lifecycle hooks" immediately understandable

2. **`server/` → `runtime/`**
   - Name explicitly states purpose: runtime orchestration
   - More idiomatic in Go ecosystem
   - No confusion with "HTTP server"

3. **Consolidated `server/` files into `runtime/`**
   - `service.go` → `runtime.go` (clearer main file)
   - Other files stay as-is but now clearly part of runtime initialization
   - Separated concerns still visible in filenames

4. **Flattened `bootstrap/providers/` → removed**
   - Functionality moved to `runtime/providers.go`
   - Registry construction is part of runtime initialization
   - Less nesting, clearer purpose

5. **Added `pkg/client/`**
   - Public Go SDK for boxy HTTP API
   - CLI can use same client external users would
   - Currently stubbed with TODOs

6. **Added `doc.go` to all packages**
   - Every package now has clear documentation
   - Explains purpose, responsibilities, and usage
   - Examples provided where appropriate

### Package Narrative

The new structure tells a clear story:

1. **`core/`** = Domain Logic
   - "What can the system do?"
   - Sandboxes, pools, allocation, lifecycle hooks
   - Pure business logic, no infrastructure concerns

2. **`runtime/`** = Application Orchestration
   - "How do we wire everything together and run it?"
   - Composition root pattern
   - Initialization, startup, shutdown sequences

3. **`api/`** = External Interfaces
   - "How do users interact with boxy?"
   - REST HTTP API

4. **`storage/`, `config/`** = Infrastructure
   - "How do we persist data and configure the system?"
   - Technical implementation details

5. **`pkg/client/`** = Public SDK
   - "How do external Go programs interact with boxy?"
   - Type-safe client library

### Dependency Flow

```
┌─────────────┐
│ cmd/boxy    │
└──────┬──────┘
       │
┌──────▼──────┐
│  runtime/   │ ← Composition root
└──────┬──────┘
       │
   ┌───┴────┬──────────┬────────┐
   ▼        ▼          ▼        ▼
┌──────┐ ┌──────┐ ┌─────────┐ ┌────┐
│ core/ │ │ api/ │ │ storage/│ │cfg │
└──────┘ └──────┘ └─────────┘ └────┘
```

Clean, acyclic dependencies with clear layering.

## Consequences

### Positive

1. **Clear narrative** - New developers can understand the structure immediately
2. **Better cohesion** - Packages have single, clear responsibilities
3. **Explicit naming** - `lifecycle`, `runtime` are self-documenting
4. **Maintainable** - Easy to know where new code belongs
5. **Well-documented** - Every package has doc.go with examples
6. **Scalable** - Structure supports growth without confusion

### Negative

1. **Migration effort** - All import paths need updating
2. **Git history** - File moves may obscure `git blame`
3. **Learning curve** - Team needs to learn new structure (mitigated by docs)

### Neutral

1. **Breaking change** - This is pre-v1.0, so acceptable
2. **Old structure removed** - `internal/server/`, `internal/hooks/`, `internal/bootstrap/` deleted after import updates

## Implementation

1. ✅ Move hooks to `core/lifecycle/hooks/`
2. ✅ Rename server to runtime
3. ✅ Flatten bootstrap/providers
4. ✅ Add doc.go to all packages
5. ✅ Stub pkg/client package
6. ✅ Create this ADR
7. ⏳ Update all import paths
8. ⏳ Verify tests still pass
9. ⏳ Remove old directories

## Notes

- The term "runtime" is idiomatic in Go (see Kubernetes, Docker)
- `core/lifecycle/` could expand to include other lifecycle concerns beyond hooks
- `pkg/client/` is stubbed; implementation tracked in client.go TODOs
- This structure follows hexagonal architecture principles without being dogmatic
