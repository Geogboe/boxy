# GitHub bug report template design

## Scope

Add a structured GitHub bug-report form in Markdown for reproducible Boxy
issues.

## Decisions

- Apply the existing `bug` and `triage` labels by default.
- Require reporters to confirm that they searched for duplicates.
- Collect version, OS/architecture, installation, provider/agent, and
  configuration-shape context without asking for secrets.
- Include explicit redaction guidance for API keys, passwords, tokens,
  hostnames, usernames, and other personally identifiable or sensitive data.
- Separate reproduction steps, expected behavior, actual behavior, and
  optional logs/screenshots so reports are actionable and safe to share.

## Acceptance criteria

- GitHub recognizes the file as a bug issue template.
- The template includes duplicate-search confirmation, environment details,
  reproducible steps, and PII/credential redaction guidance.
- This documentation-only change does not affect the release binary.
