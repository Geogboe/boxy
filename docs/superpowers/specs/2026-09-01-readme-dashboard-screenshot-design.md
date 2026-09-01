# README dashboard screenshot (#286)

README includes a checked-in PNG captured from the authenticated dashboard in
the `examples/devfactory-containers` environment. The capture is made with
Firefox at a desktop viewport and shows the operator Pools page, including
capacity counts, resource state rows, drain/fill controls, diagnostics, and
cleanup controls.

The asset is documentation-only: it contains no real provider credentials,
guest secrets, or external infrastructure identifiers. A repository test
checks that the PNG decodes, stays within a small documentation-friendly size,
and remains referenced by the README.
