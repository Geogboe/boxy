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
| Grouped summary/pool row | `.pool-group-primary` (identity + status) and `.pool-group-secondary` (counts, muted via `.pool-total-muted`, and an amber `.pool-ready-warning` when under `min_ready`) |
| Row-level actions revealed on hover/focus | `.row-actions-menu` and `.row-actions-toggle`; keep the fixed-width actions column so reveal never reflows other columns, and drive visibility with `opacity` + `:focus-within` (never `visibility`/`display`) so keyboard tab focus still reaches the actions without a pointer |

Pool and sandbox details are collapsed by default when they contain a list.
Historical records belong behind an explicit filter, while active resources
remain the first view.

## Accessibility and responsive behavior

Every action must be reachable with the keyboard and have a visible text or
accessible label. Links that open the repository in another tab use
`rel="noreferrer noopener"`. Inputs retain labels, buttons describe their action, and
status text remains present when color changes between themes.

Use flex or grid with wrapping for page controls. Keep tables in
`.table-scroll`, preserve readable minimum column widths, and avoid hiding
important values behind hover-only interactions. Respect
`prefers-reduced-motion` for animations. When adding a visual component, update
`internal/server/ui_design_test.go` with its stable selector and this document
with its intended use so stylesheet drift is caught in server tests.

## Client-side state and the 5-second poll

Any page with `hx-trigger="every 5s"` swapping a container's `innerHTML`
(`#pools-fragment` is the current example) replaces every DOM node inside
that container on each poll — including nodes a user just interacted with.
**Anything browser-native that isn't re-derived from the server response is
lost on the very next poll**: an expanded native `<details open>`, scroll
position within the fragment, focus, an in-progress but unsubmitted form
edit. This is easy to miss because a quick manual click-and-screenshot test
looks correct — the bug only shows up after waiting a full poll cycle with
no further interaction.

The Pools page hit this for real (#327, 2026-09-09): expanding a pool row
worked, then silently collapsed on the next poll. The fix, if you need the
same pattern elsewhere:

1. Give the element a stable `id` derived from data that survives the swap
   unchanged (e.g. `pool-group-<name>`, not an array index).
2. Add one `htmx:beforeSwap` listener (scoped to the specific fragment
   container by checking `e.detail.target.id`) that records which stateful
   elements are currently "on" by id, and one `htmx:afterSwap` listener that
   re-applies that state to matching ids in the newly-swapped content.
3. Verify it live, not just with a unit test: expand/interact, wait past a
   full poll cycle (not just take an immediate screenshot), then check the
   state is still there. A Go template test can assert the `id` attribute
   and the listener script are present, but only a real browser exercises
   the actual swap-and-reapply behavior.

Don't reach for `hx-preserve` for this — it freezes the element's content
across swaps entirely, which defeats the point of polling for genuinely
fresh data (e.g. resource counts inside an expanded row).

## Validating a UI change against its mockups

When implementing a UI change from a design/mockup (an issue with attached
screenshots, a Figma link, etc.), a green `internal/server/ui_test.go` run
is necessary but not sufficient — those tests assert specific rendered
strings/classes, not the actual visual result, and won't catch a poll-state
bug like the one above, a layout deviation from the mockup, or a design
decision two issues in the same batch quietly contradict. Do a live
browser pass before calling the work done:

1. Run `task serve:ui` (a `boxy serve` against
   `examples/devfactory-containers/boxy.yaml`, which simulates a pool with
   live churn without needing real infrastructure).
2. Drive it with `playwright-cli` (see the bundled `playwright-cli` skill),
   log in with the bootstrapped admin password
   (`.boxy/bootstrap-admin-password` next to the config), and screenshot the
   actual page next to the mockup image side by side.
3. Exercise interactive elements the mockup implies (expand a row, wait past
   a poll cycle, submit a form after a poll) — not just the initial static
   render.
4. When a deviation is a genuine design fork between two things the user
   cares about (not just an implementation slip), surface it and ask rather
   than silently picking a side — see #327 vs #328's status-badge tension,
   resolved by folding a count into muted text rather than dropping either
   issue's goal.
