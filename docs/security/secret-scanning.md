# Secret-scanning fixture convention

Boxy scans the complete Git history with Betterleaks. The scan is intentionally
not limited to the current filesystem, because a credential that was committed
and later removed is still a repository exposure.

## Configuration layers

`.betterleaks.toml` and `.betterleaksignore` are source-controlled Boxy policy.
Betterleaks reads them from the repository root during local and CI scans; they
are not part of the Boxy application binary. The project configuration uses
`[extend] useDefault = true`, so it layers a small set of repository-specific
path rules over Betterleaks's embedded default rules. Keep the pinned
Betterleaks version and the small override in sync rather than copying the
tool's generated default configuration into this repository.

## Fake credentials in tests and documentation

Use an environment-shaped placeholder whenever a test or example needs a
credential-shaped value:

```text
${BOXY_TEST_PASSWORD}
${BOXY_TEST_TOKEN}
${BOXY_TEST_API_KEY}
```

These are literal fixture values; Boxy does not expand them unless the example
explicitly describes another substitution step. Betterleaks recognizes this
shape as a placeholder without requiring a project-specific allowlist.

For credential-bearing URLs, use a reserved example host as well:

```text
https://example-user:${BOXY_TEST_PASSWORD}@example.invalid/service
```

Do not use `password`, `changeme`, `testpass`, `foo`, `bar`, or random-looking
strings as fake credentials. They can be valid real-world credentials and
should remain detectable. Use a distinct placeholder name when a test needs to
distinguish bootstrap, rotated, or API credentials.

## Historical findings

Existing historical fixtures cannot be removed from shared Git history by
changing the current file. After reviewing a finding as synthetic, record only
its Betterleaks fingerprint in `.betterleaksignore`; never commit the finding's
secret or match text. Keep those entries narrow and do not suppress an entire
path or rule, so new credentials in the same file remain visible.
