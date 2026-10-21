package subprocess

// fillContentHash sets the ContentHash of a source-based extension that has none to the hash of its source files, so the cell planner
// invalidates a packed cell's cache when the source changes, and records the check a build runs before it publishes the cell: the
// source must still hash to it (see sourceUnchanged). A compiler reads the source after this hash, past the wait for a peer's build
// lock and through its own compile, and a cell built from a source edited since would be cached under the hash of the original.
func fillContentHash(cfg *ExtConfig) {
	if cfg.Source == "" || cfg.ContentHash != "" {
		return
	}
	buildType, err := detectBuildType(cfg.Source)
	if err != nil {
		return
	}
	digest, stamp, err := hashSourceDirStamped(cfg.Source, buildType)
	if err != nil {
		return
	}
	cfg.ContentHash = digest
	source := cfg.Source
	cfg.sourceCheck = func() error { return sourceUnchanged(source, buildType, stamp) }
}
