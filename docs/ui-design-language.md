# Boxy web UI design language

The Boxy dashboard uses a small, server-rendered design language. The goal is
to make operational information easy to scan while keeping every page usable
on a narrow screen and with a keyboard. New dashboard work should reuse this
vocabulary instead of introducing page-specific visual rules.

## Foundations

- **Color tokens:** `--bg`, `--bg-card`, `--bg-table-row`, `--border`,
  `--text`, `--text-muted`, `--accent`, and the semantic status colors
  `--green`, `--yellow`, `--red`, and `--orange` are the only palette entry
  points. Dark mode is the default; `data-theme="light"` swaps the same
  tokens for the light palette.
- **Typography:** use the system UI stack already defined on `body`. Page
  titles and table headers carry hierarchy through size and weight, not a new
  font family.
- **Shape and spacing:** cards use the shared border and radius treatment;
  controls use the same compact padding and focusable hit area. Prefer the
  existing spacing rhythm over one-off pixel values.
- **Status:** use `.badge` with a semantic modifier such as
  `.badge-ready`, `.badge-error`, or `.badge-drained`. Status color must not
  be the only indication of state; include text as well.

## Layout and navigation

Full pages use `.layout`, with a persistent `.sidebar` and a bounded `.main`
content column. The sidebar brand links home and the active navigation item is
marked with both color and `aria-current`. Admin-only links are rendered only
when the session has the matching capability.

Pages begin with `.page-header` and `.page-title`. Group operational content
in `.table-card` sections. A `.table-scroll` wrapper is the standard way to
keep wide tables usable without forcing the whole page to overflow. The home
page uses `.stats` for counts and `.quick-link-card` for direct paths to the
primary operational surfaces.

## Component vocabulary

Use these existing classes before adding a new one:

| Need | Component |
| --- | --- |
| Primary or secondary action | `.button-link` and `.button-link.secondary` |
| Form or destructive action | `.secondary-btn` or `.danger-btn` |
| Compact state | `.badge` plus a semantic modifier |
| Filter choice | `.filter-pills` and `.filter-pill` |
| Expandable detail | native `<details>` with `.pool-group` or `.sandbox-detail` |
| Empty state | `.empty` |
| Long inventory | `.table-card` plus `.table-scroll` |
| Background request | `.htmx-indicator` and the existing refresh styles |

Pool and sandbox details are collapsed by default when they contain a list.
Historical records belong behind an explicit filter, while active resources
remain the first view.

## Accessibility and responsive behavior

Every action must be reachable with the keyboard and have a visible text or
accessible label. Links that open the repository in another tab use
`rel="noreferrer"`. Inputs retain labels, buttons describe their action, and
status text remains present when color changes between themes.

Use flex or grid with wrapping for page controls. Keep tables in
`.table-scroll`, preserve readable minimum column widths, and avoid hiding
important values behind hover-only interactions. Respect
`prefers-reduced-motion` for animations. When adding a visual component, update
`internal/server/ui_design_test.go` with its stable selector and this document
with its intended use so stylesheet drift is caught in server tests.
