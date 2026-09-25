package runtimecell

// Prebuilt-cell resolution lets a host skip building a packed cell from source
// when an already-built binary for the exact composition is available.
//
// This exists because a packed cell's from-source cache key
// (goPackedCellHash / rustPackedCellHash) folds in the host toolchain version
// (`go version`, `rustc --version`) and omits the build target. On a machine
// with no toolchain those version probes fail, so the from-source hash is not
// reproducible there and a binary "seeded" into the from-source cache path is
// never found. Resolving instead addresses a cell by its composition
// (language + key + extensions + target), which is stable across machines and
// needs no toolchain.
//
// The default resolver is nil: stock pig builds every cell from source exactly
// as before. A Piglet Binary binary registers a resolver at init that serves its
// embedded, extracted cells.

// PrebuiltExtension identifies one extension inside a cell composition.
type PrebuiltExtension struct {
	Name string
	Hash string
}

// PrebuiltRequest describes the packed cell the host is about to build.
type PrebuiltRequest struct {
	Language   string // "go" | "rust" | "python"
	Key        string // cell key (pack group)
	GOOS       string
	GOARCH     string
	Extensions []PrebuiltExtension
}

// PrebuiltResolver maps a cell composition to an already-built binary. The
// resolver owns the match policy (e.g. how strictly to compare pinned hashes);
// the host only supplies the composition it is about to build.
type PrebuiltResolver interface {
	ResolvePrebuilt(PrebuiltRequest) (binaryPath string, ok bool)
}

var prebuiltResolver PrebuiltResolver

// SetPrebuiltResolver installs the process-wide prebuilt-cell resolver, or nil
// to restore the default from-source behavior.
func SetPrebuiltResolver(r PrebuiltResolver) { prebuiltResolver = r }

// ResolvePrebuilt exposes the process-wide prebuilt-cell resolver to other host
// packages (e.g. the source-mode subprocess builder). Returns ("", false) when
// no resolver is registered, so stock pig behavior is unchanged.
func ResolvePrebuilt(req PrebuiltRequest) (string, bool) { return resolvePrebuilt(req) }

func resolvePrebuilt(req PrebuiltRequest) (string, bool) {
	if prebuiltResolver == nil {
		return "", false
	}
	return prebuiltResolver.ResolvePrebuilt(req)
}

func goPrebuiltExtensions(exts []GoExtension) []PrebuiltExtension {
	out := make([]PrebuiltExtension, len(exts))
	for i, e := range exts {
		out[i] = PrebuiltExtension{Name: e.Name, Hash: e.Hash}
	}
	return out
}

func rustPrebuiltExtensions(exts []RustExtension) []PrebuiltExtension {
	out := make([]PrebuiltExtension, len(exts))
	for i, e := range exts {
		out[i] = PrebuiltExtension{Name: e.Name, Hash: e.Hash}
	}
	return out
}
