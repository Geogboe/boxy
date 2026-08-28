# Design (exploratory): resource templates, guest configuration, and pool-to-pool promotion

Status: Exploratory — captures a live design conversation, not an
implementation-ready spec. Several sections end in an open question rather
than a decision. Nothing here should be built until the open questions are
resolved.
Date: 2026-08-28
Tracks: #234 (started narrower — "resource templates" — and grew into this
during discussion)

## How we got here (keep for context, don't re-litigate)

#234's issue text ("allow you to build and template our resources to go
into a pool... maybe use something else to orchestrate builds though?") was
originally scoped down to "a named, reusable preset for `PoolSpec.Config`"
— pure DRY, no new machinery. Talking it through surfaced a bigger, real
scenario: three Windows Server VM pools that share a common base but differ
in installed apps (pool A has app1+app2, pool B needs app1+app2+app3), and
the owner wants pool B able to take a ready pool-A resource and layer app3
on top instead of provisioning app1+app2+app3 from scratch every time. That
is not "templating a resource config" — it's **incremental guest
configuration** plus **moving a resource from one pool's inventory into
another's**, neither of which exists in Boxy today. This document is about
that larger thing.

Two claims from the conversation, checked against the code, corrected here
so the rest of this document doesn't build on a wrong premise:

- **Cross-pool inventory sharing is not already established.**
  `internal/sandbox/fulfiller.go`'s `matchPool` requires an exact
  one-to-one match between a request's `(Type, Profile)` and a single
  pool's `Inventory.ExpectedType`/`ExpectedProfile` — zero matches or more
  than one both error. Every pool is fully self-contained today.
- **This is explicitly the gap ADR-0002 left open.** ADR-0002 ("Resources
  Never Return to a Pool") states resources are single-use and anticipates
  that *"any future 'reuse' capability would need a new concept and ADR."*
  Pool-to-pool promotion is that capability. It is not "returning to a
  pool" (the resource never goes back to pool A once claimed by pool B) —
  it's a new, one-directional move that ADR-0002 didn't rule out, just
  didn't design.

## Vocabulary this conversation introduced

- **Resource template**: a named, reusable definition of a resource's
  shape — base provider config plus a chain of configuration steps applied
  after creation. A pool references a template instead of (or in addition
  to) inlining its own `Config`.
- **Derivation**: a template can build on another template (`derives_from:
  win-server-base-apps12`), inheriting its base config and configuration
  steps, then adding more on top. Pool A's template and pool B's template
  in the motivating example are related this way.
- **Promotion**: pool B claiming a *ready* resource whose lineage traces
  back through pool B's template's ancestor chain (e.g. a pool-A resource,
  since B's template derives from A's), then running only the *delta*
  configuration steps (B's steps minus the ancestor's already-applied
  steps) to bring it up to B's spec, then folding it into B's inventory.
  This is a new resource lifecycle event, not sandbox allocation and not
  destruction.
- **Guest configurer** (name not settled — see Naming below): a new,
  pluggable capability for "bring a guest from state A to state B" — e.g.
  install/configure software — distinct from resource *creation*
  (`providersdk.Driver.Create`) and from `providersdk.GuestPersonalizer`
  (which is narrowly allocation-time credential rotation only, not general
  configuration; see its doc comment in `pkg/providersdk/guest_personalization.go`).

## Proposed shape

### Templates form a tree, not a graph — and that's enough

`derives_from` is single-parent in every example discussed. A template
tree needs ancestor-chain resolution ("what steps has this template
already accumulated") and cycle rejection ("does this derive_from chain
loop back on itself") — both a `for` loop over a map with a visited set,
maybe 20 lines, belonging in `internal/`. This does **not** justify a new
`pkg/graph` primitive: AGENTS.md is explicit that a `pkg/` package
shouldn't be shaped by exactly one consumer, and a graph library whose
only caller is template lineage would be shaped by template lineage.
Revisit if templates ever need multiple parents (composing two unrelated
template lineages) or a second unrelated consumer shows up (e.g. tracing
provider/agent dependencies was floated once, elsewhere, but isn't a
concrete need today) — that's the point at which extracting a real `pkg/`
primitive from the (by-then-duplicated) traversal logic is justified, not
before.

This gives the owner's "easily traceable dependencies" goal directly: the
tree *is* the trace. "What does template X derive from" and "what
templates derive from X" are both cheap queries over that tree without any
new abstraction.

### The "invisible system base pool" — recommend: no synthetic record

Two ways to give every pool "the same architectural shape, all deriving
from something":

- **(a) Literal base pool**: an actual `Pool` record every template chain
  roots at, that every reconciler, `ListPools` caller, and `matchPool`
  path has to learn to recognize and skip (it presumably never serves
  sandbox requests directly).
- **(b) Implicit root**: "no `derives_from`" *is* the root. The "base
  pool" is a documentation/mental-model concept ("this pool has no
  ancestor, so it provisions everything from its own driver from
  scratch"), not a stored entity.

Recommend (b): it gets the stated goal (every pool is describable the same
way — "derives from X" or "derives from nothing") without a synthetic
record every other pool/resource code path must special-case. Flagged as
an open question, not a settled decision, since "keep our architecture
consistent" was explicitly the owner's own reason for wanting (a).

### A new pluggable capability, not folded into `providersdk`

The owner's instinct — "boxy should rely on a new type, similar to how we
have drivers and providers... maybe the first implementation is ansible" —
is architecturally right: this is a distinct concern from resource
*creation* (`providersdk.Driver`) and deserves its own contract + registry,
mirroring how `providersdk` and `agentsdk` are each scoped to one concern.

**Naming collision to resolve before writing code**: `internal/pool`
already has `Provisioner`, `AgentProvisioner`, `LockedProvisioner`,
`DriverProvisioner`, and `ProvisionLocker` — all meaning "the thing that
*creates* a resource." A second, unrelated `Provisioner` interface meaning
"the thing that *configures* a resource" in a new package would be a real
readability trap in a codebase that already overloads the word. Candidate
alternatives, none chosen yet: `configuresdk`/`Configurer`,
`bakesdk`/`Baker` (image-layering metaphor), `guestconfig`/`Applier`. Pick
one before implementation starts.

Sketch of the contract (illustrative, not final):

```go
package configuresdk // name TBD

// Target is what a configuration step runs against — the same connection
// shape providers already use to reach a guest (host + credential +
// transport hint), not a new one.
type Target struct {
    ResourceID string
    // ... connection details, reusing existing guest-credential/exec
    // plumbing rather than inventing a second one.
}

type Step struct {
    Kind string         // e.g. "ansible", "dsc", "shell"
    Spec map[string]any // kind-specific payload (playbook ref, script, ...)
}

// Configurer applies one Step to a Target. Implementations are
// kind-specific, discovered via a registry the same way providersdk.Driver
// implementations are.
type Configurer interface {
    Apply(ctx context.Context, target Target, step Step) error
}
```

### The Ansible-over-PSRP problem — the biggest open technical risk

"First implementation is Ansible" needs a concrete target-connection
answer, and the obvious one doesn't work cleanly for Boxy's actual Windows
story:

- Ansible's Windows story is WinRM (or SSH on newer Windows) — it does not
  speak PSRP-over-HvSocket, which is exactly what `pkg/psdirect` gives
  Boxy (guest exec **with no guest networking required**, the entire point
  of #222/#223's range-based IP allocation work being optional rather than
  load-bearing).
- Three real options, different costs:
  1. **Guest gets a routable IP, Ansible connects over WinRM directly.**
     Simplest to build, but abandons the no-network-required advantage
     PowerShell Direct exists for.
  2. **A custom Ansible connection plugin that proxies through Boxy's own
     exec transport.** Real engineering, and in Python — a second language
     surface this Go-only project doesn't have today.
  3. **Ansible targets Linux/SSH guests only** (`pkg/vmsdk`'s existing SSH
     exec path already used for Linux). **Windows guests get a separate,
     PowerShell/DSC-based `Configurer` implementation** that runs directly
     over the existing `psdirect` transport, with no Ansible involved for
     Windows at all.

**Recommend option 3 as the honest v1** — it reuses transport Boxy already
has for both guest OSes instead of adding a new one, and "Ansible" stops
being "the" first implementation and becomes "the Linux-guest
implementation," with a separate Windows-guest implementation alongside
it. This changes what the owner's "first impl is ansible" framing means in
practice — worth an explicit yes/no before building either one.

### Promotion breaks a real, already-documented capacity invariant

AGENTS.md documents that `pool.preheat.max_total` is enforced against
*all* non-destroyed resources with a given `OriginPool`, and that
`OriginPool` is "immutable provenance." Promotion directly collides with
both:

- If pool B drains pool A's ready inventory via promotion, pool A's
  `computeToProvisionCount` sees the same gap an ordinary sandbox
  allocation would have left and re-provisions to refill — meaning a
  promotion-heavy pool B silently drives pool A's ongoing provisioning
  volume. Pool A's `min_ready` was sized to serve pool A's own sandbox
  demand; it is now implicitly also sizing pool B's supply chain, with no
  visibility of that at pool A's config.
- A resource promoted out of pool A still counts against pool A's
  `max_total` forever, per the "immutable `OriginPool`" rule — unless
  promotion is allowed to mutate it, which is exactly what ADR-0002 calls
  immutable, for provenance reasons that still matter (e.g. guest
  credential bootstrap authorization is scoped by `OriginPool` +
  `Provider.AgentID` today — see the Guest Credentials section of
  AGENTS.md).

**This needs an explicit position, not an implementation-time
discovery**: does promotion release the resource from pool A's
`max_total` accounting? Does `OriginPool` stay immutable (provenance) with
a *separate* new field recording promotion lineage/current-pool
membership, so the credential-authorization check and the capacity check
can use different fields for different purposes? This is real ADR
material — likely its own ADR (a sibling to ADR-0002, not a silent
amendment to it), not something folded into this exploratory doc.

## Consequences / scope this touches

- `model.Pool`/`config.PoolSpec` gain a template reference.
- A new `model.ResourceTemplate` (or similar) type: name, optional
  `derives_from`, base config, ordered configuration steps.
- A new SDK package (name TBD, see Naming) for the `Configurer` contract +
  registry, parallel to `providersdk`/`agentsdk`.
- At least one new resource transient state for promotion-in-progress,
  mirroring the existing `recycling`/`destroying` pattern (AGENTS.md) so a
  resource mid-promotion is observable via the REST API instead of
  vanishing from one pool and reappearing in another with no visible
  in-between state.
- A new ADR for the capacity/provenance questions above — a sibling to
  ADR-0002, since ADR-0002 explicitly deferred this rather than settled
  it.
- `internal/pool`'s fill/reconcile logic needs a promotion path alongside
  its existing from-scratch provisioning path.

## Open questions (all unresolved — pick before implementing)

1. **Naming**: what does the new SDK package/interface get called, given
   `internal/pool.Provisioner` already means something else?
2. **Base pool**: implicit root (recommended) vs. a literal synthetic
   `Pool` record?
3. **First `Configurer` implementation(s)**: Ansible-for-Linux +
   separate-Windows-implementation (recommended), vs. Ansible-over-WinRM
   for both (abandons the no-network PowerShell-Direct story), vs. a
   custom Ansible connection plugin (adds a Python surface)?
4. **Promotion vs. capacity accounting**: does `OriginPool` stay immutable
   with a new lineage field, or does promotion mutate it? Does a promoted
   resource still count against its origin pool's `max_total`?
5. **Multi-parent templates**: confirmed out of scope per this
   conversation (tree, not graph) — revisit only if a real need for
   composing two unrelated template lineages shows up.

## Suggested phased build order (once the above is settled)

1. `model.ResourceTemplate` + `derives_from` tree + pool references a
   template — pure DRY, no promotion, no new SDK package yet. Delivers the
   original narrow #234 ask on its own.
2. New `Configurer` SDK package + registry + one real implementation
   (Linux/Ansible or Windows/PowerShell, whichever the owner wants first)
   — configuration steps run once, at resource-creation time, within a
   single pool. No cross-pool promotion yet.
3. Cross-pool promotion, gated on the new ADR resolving the capacity/
   provenance questions above, plus the new transient resource state.

## Scope note for this session

This document and `2026-08-28-oidc-ui-and-cli-auth-design.md` are both
exploratory/design specs written this session — **neither has any
accompanying implementation**. `dev` gained zero new production code in
this half of the session; the deliverable is two durable design documents
for review before either becomes real work.
