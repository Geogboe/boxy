# Design: privacy-scoped API-key management (#272)

## Scope

Boxy has two bearer-key kinds: short-lived personal keys tied to a human
identity and admin-issued service keys used by automation. The dashboard must
make the ownership boundary visible without turning the key inventory into a
credential-disclosure surface.

## Decisions

- A profile session can list metadata only for personal keys whose stable
  subject matches that session. It cannot see another person's personal keys
  or any service-key inventory.
- The admin service-key page and admin REST list contain service keys only.
  Existing records with an empty `Kind` remain service keys for compatibility.
- Service-key creation accepts a name, role, and optional positive expiry
  duration. The existing admin-only API remains the programmatic interface;
  the UI uses an equivalent session-protected form.
- Raw keys are rendered only in the immediate create response and are absent
  from all persisted models, metadata listings, subsequent page loads, and
  logs. Revoke is idempotent and never returns the raw key. The HTML revoke
  form redirects back to the service-key page after success so the rendered
  status reflects the revocation.
- Personal-key revocation is not added here; short TTLs and owner-scoped
  visibility remain the v1 boundary.

## Acceptance

- OIDC/local identities see only their own personal-key metadata.
- Non-admin sessions cannot open service-key management.
- Admins can create and revoke service keys, including repeated revocation.
- HTML revocation returns to the service-key page with updated status.
- Positive expiry validation is shared by the UI and REST create paths.
