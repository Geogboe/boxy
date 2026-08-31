# Boxy favicon design

## Scope

Add a small embedded SVG favicon to the authenticated dashboard and reference
it from the shared page layout.

## Decisions

- Reuse the dashboard's `--accent` (`#6c8cff`) and `--bg` (`#0f1117`) colors.
- Use a simple isometric box mark so the favicon remains recognizable at small
  sizes and reflects Boxy's resource-pooling purpose.
- Keep the asset local and static so it works with the existing embedded
  `static/*` filesystem and requires no external request or runtime generation.

## Acceptance criteria

- `GET /static/favicon.svg` returns the embedded SVG with an SVG content type.
- Full dashboard pages link the asset from `/static/favicon.svg`.
- The asset contains no user, provider, credential, or environment data.
