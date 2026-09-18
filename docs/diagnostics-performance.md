# Diagnostic append performance

Provider logging runs synchronously in the operation that emits an event.
Reading, normalizing, and rewriting the entire retained history on every append
therefore makes operation latency grow with log history.

Keep a metadata-validated append snapshot. An append that does not evict events
can write one record; age or byte eviction still rewrites the retained snapshot.
Buffer those rewrites rather than issuing one file write per event. A changed
file or a new store must reload and normalize existing records before trusting
the snapshot. Preserve event ordering, redaction, restart behavior, and the
existing age and byte retention rules, including oversized single events.

Benchmark both growing history and a full retention window. Report cold snapshot
loading separately from warmed appends; neither case may be silently excluded
from the measurements.

With 10,000 synthetic retained events and ten measured appends per case, a
Windows host comparison reduced warmed appends below the byte limit from
632 ms to 1.1 ms. At the byte limit, where every append evicted an old event,
buffered rewrites reduced the measured cost from 626 ms to 40 ms. Full-window
rewrites still scale with retained history; this does not make them constant
time. The first append after startup or an external file change still loads and
normalizes existing history. Live application validation is separate from this
synthetic comparison.
