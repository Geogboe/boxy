package providersdk

// RelativePathResolver is an optional capability for a provider Config type
// that has one or more filesystem path fields it wants resolved relative to
// the boxy config file's own directory, rather than left to resolve against
// whatever the process's ambient working directory happens to be. This
// mirrors how Boxy's own .boxy/state.json is resolved (see
// internal/cli/serve.go's serveStatePath) — a relative path in a config
// file should mean the same thing regardless of where the process was
// launched from.
//
// Registry.NewDriverFromInstance calls ResolveRelativePaths (if the decoded
// config implements it) with baseDir before constructing the driver.
// baseDir is empty when no config file path is known (e.g. no --config
// flag, or config loaded from stdin); implementations should leave their
// paths untouched in that case rather than guessing a base.
//
// Optional and type-asserted, like every other capability in this package
// (ResourceLister, AvailabilityReporter, GuestPersonalizer, ErrorTyper) —
// most provider Config types have no config-relative path fields and don't
// implement this. It's deliberately not the default for every path-shaped
// config field: docker's socket path and hyperv's VHD/template paths are
// real host filesystem locations an operator points at explicitly, not
// directories conceptually owned by the boxy config file the way
// devfactory's DataDir is.
type RelativePathResolver interface {
	ResolveRelativePaths(baseDir string)
}
