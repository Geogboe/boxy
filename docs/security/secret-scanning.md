# Secret and PII scanning fixture convention

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

## Controlled PII fixture convention

Secret scanning and PII scanning are separate checks. `.betterleaks-pii.toml`
contains the repository's narrow PII rules; it does not replace the default
secret rules in `.betterleaks.toml`.

Use controlled values whenever tests, examples, plans, or documentation need
identity-shaped data:

```text
boxy-test@example.invalid
boxy.example.test
192.0.2.5
198.51.100.5
203.0.113.5
2001:db8::5
boxy-test-user
C:\Users\boxy-test-user\...
/home/boxy-test-user/...
```

The reserved example domains and documentation/test IP ranges are deliberate.
Use them instead of real-looking email addresses, private network addresses,
personal hostnames, usernames, or home-directory paths. Platform identities
that are part of a documented provider contract, such as `Administrator`,
`root`, or `ubuntu`, may remain when they are not personal data.

Run the repository-history PII scan with:

```text
task pii:scan
```

To review an issue, pull request, or public comment before publishing it,
write the text to a temporary local file and scan it through stdin:

```powershell
Get-Content .\issue-body.md -Raw | task pii:scan:stdin
task pii:authors
```

On POSIX shells, use `cat issue-body.md | task pii:scan:stdin`. The author
report is informational and non-blocking: Git author metadata is reviewed
separately from repository content. The content scan is blocking and covers
the full Git history, so it can catch data removed from the current tree.

## Historical findings

Existing historical fixtures cannot be removed from shared Git history by
changing the current file. After reviewing a finding as synthetic, record only
its Betterleaks fingerprint in `.betterleaksignore`; never commit the finding's
secret or match text. Keep those entries narrow and do not suppress an entire
path or rule, so new credentials in the same file remain visible.

The same rule applies to historical PII fixtures. Add a fingerprint only for
an individually reviewed, synthetic value that cannot be removed from shared
history. Do not use `.betterleaksignore` to hide a real address, hostname,
username, or home path.
