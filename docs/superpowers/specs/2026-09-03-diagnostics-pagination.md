# Diagnostics UI pagination

## Decision

The diagnostics page uses the diagnostics store's existing opaque cursor for
server-side pagination. The page keeps the current filters in the next-page
link and renders one bounded result page at a time. It does not introduce
client-side polling or a second asynchronous data path yet; those would add a
different interaction model without reducing the store query cost.

## User experience

- The first page keeps the current filter form and export action unchanged.
- When more events match, the page shows a `Next page` link below the table.
- The link carries `level`, `component`, `pool`, `agent`, `resource`, `since`,
  and `limit` alongside the opaque cursor.
- The export action deliberately excludes the cursor so it exports the full
  filtered result set rather than only the visible page.
- The final page has no next-page link.

## Acceptance criteria

- A filtered page with more results than its limit renders only that bounded
  page and exposes a next-page link.
- Following the link returns the next page without losing any active filter.
- The link disappears when no further events exist.
- Cursor values remain opaque to the UI; the server continues to validate and
  decode them through the diagnostics store.
