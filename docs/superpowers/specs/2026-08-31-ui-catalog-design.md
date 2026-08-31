# Design: startup catalog view for the Boxy UI (#274)

## Scope

The dashboard needs one read-only view of the configured resource templates,
packages, artifact sources, artifact stores, and the relationships that connect
those records to pools. The catalog is configuration state, not mutable runtime
state, so the daemon takes a snapshot at startup and passes it to the HTTP
server through a narrow `CatalogSource` seam.

## Decisions

- `CatalogSource` loads a `CatalogSnapshot`; the server does not import the
  configuration package or inspect provider-specific configuration.
- `NewStaticCatalogSource` clones and sorts the snapshot. Existing server
  constructors remain compatible when no catalog is supplied.
- Catalog view models use an allowlist: template shape and references,
  package identity/method/scope/event, source location/integrity metadata, and
  store type/location metadata. Template config, package inputs/defaults,
  source metadata, and store credentials are not represented in the view model
  and therefore cannot be rendered accidentally. Store endpoints are reduced
  to scheme/host/port/path; userinfo, query strings, and fragments are removed
  before the snapshot crosses into the server.
- Missing references are displayed as non-fatal relationship warnings. A
  catalog load failure is rendered as a generic retryable state; the underlying
  error is not sent to the browser.
- `/ui/catalog` is protected by the existing session middleware and has no
  mutating action or API equivalent in this release.
- Daemon startup remains strict: configuration validation and pool resolution
  reject invalid references before serving. Relationship warnings remain a
  defensive contract for injected, stale, or future `CatalogSource`
  implementations.

## Acceptance

- Empty and populated catalogs render stable, name-sorted sections.
- Injected or stale catalog snapshots identify missing templates, packages,
  sources, and stores without weakening daemon startup validation.
- A session is required, and load failures do not disclose error details.
- The daemon's config-to-snapshot conversion cannot expose secret-bearing
  configuration fields, including credentials or signed URL query parameters
  embedded in artifact-store endpoints.
