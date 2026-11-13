package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// FusedResolver maps an extension config to an in-process serve func compiled
// into the binary. A forged Piglet Binary (fused Piglet Binary) installs one whose serve funcs
// are each fused extension's factory().RunWithConn; the resolver is nil in stock
// pig, so LoadAll routes every extension through the subprocess cell path
// unchanged (D31).
type FusedResolver interface {
	// FusedServe returns the in-process serve func for cfg if this Piglet Binary
	// compiled that extension in, and false otherwise.
	FusedServe(cfg ExtConfig) (serve func(net.Conn) error, ok bool)
}

// FusedSourceResolver is the FusedResolver of a Piglet Binary built with `build.fusedReload: source`: it also knows the digest of
// the source each fused member was compiled from. The host then runs a member whose configured source no longer has that digest
// as a subprocess built from the source, as it runs any source extension, instead of the compiled copy (see resolveFused).
// pig additive (D31): a fused member whose source changed runs from its source.
type FusedSourceResolver interface {
	FusedResolver
	// BakedSourceDigest returns the FusedSourceDigest of the source the fused member name was compiled from, and false for a
	// member the build baked none for.
	BakedSourceDigest(name string) (digest string, ok bool)
}

var fusedResolver FusedResolver

// SetFusedResolver installs the process-wide fused-extension resolver. Called
// once at startup by a Piglet Binary's generated registration; nil (the default)
// disables fusion. Not safe for concurrent use with LoadAll.
func SetFusedResolver(r FusedResolver) { fusedResolver = r }

// RunsFused reports whether the host serves cfg from the Piglet Binary's own code, in this process, as LoadAll and a reload do. Such an
// extension reads no source and builds nothing, so it needs no staged SDK. It is false in stock pig, which installs no resolver.
func RunsFused(cfg ExtConfig) bool {
	if fusedResolver == nil {
		return false
	}
	return resolveFused(cfg).serve != nil
}

// FusedSourceDigest is the digest of a fused member's source that a Piglet Binary build bakes and the host compares on a load. It is
// the content of the source set `go build` reads from the module at dir (runtimecell.GoModuleContentDigest: go.mod, go.sum, the
// non-test sources of every package and the files they embed; no _test.go file, testdata, vendor, nested module or dot directory)
// and of each module its go.mod replaces with a local directory, keyed by module path. No file time and no absolute path enters it,
// so a touched file and a checkout at another path keep it. Pig and its SDK are left out: a fused member links the Binary's own.
// The build and the host call this one function.
func FusedSourceDigest(dir string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return "", err
	}
	modPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return "", err
	}
	mod, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		return "", err
	}
	own, err := runtimecell.GoModuleContentDigest(root)
	if err != nil {
		return "", err
	}
	var replaced []string
	for _, r := range mod.Replace {
		if r.New.Version != "" || extsource.IsGoSDKModulePath(r.Old.Path) || r.Old.Path == pigModulePath {
			continue
		}
		target := r.New.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		digest, err := runtimecell.GoModuleContentDigest(target)
		if err != nil {
			return "", fmt.Errorf("local replacement of %s: %w", r.Old.Path, err)
		}
		replaced = append(replaced, r.Old.Path+"\x00"+r.Old.Version+"\x00"+digest)
	}
	slices.Sort(replaced)
	h := sha256.New()
	h.Write([]byte(own))
	for _, entry := range replaced {
		h.Write([]byte{0})
		h.Write([]byte(entry))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pigModulePath is Pig's own module, which holds the Go SDK module.
var pigModulePath = strings.TrimSuffix(extsource.GoSDKModulePath, "/extensions/sdk")

// fusedChoice is how the host runs an extension a Piglet Binary may have compiled in.
type fusedChoice struct {
	// serve is the compiled copy's serve func; nil for an extension that runs as a subprocess cell.
	serve func(net.Conn) error
	// rebuiltFrom is the source directory of a fused member that runs as a subprocess because its source changed.
	rebuiltFrom string
	// degraded says, for a fused member that runs its compiled copy although its source differs or cannot be read, why: reason is the
	// placement `/reload --explain` shows and warning the one line a start and a reload report.
	reason, warning string
}

// FusedSourceWarning is the load error of a fused member that is loaded and runs its compiled copy although its configured source
// differs from the one it was compiled from, or cannot be read. A start reports it where it reports a failed load.
type FusedSourceWarning struct{ Message string }

func (w *FusedSourceWarning) Error() string { return w.Message }

// fusedPlacement is the strategy `/reload --explain` shows for a fused member it has something to say about; a fused member has no cell.
const fusedPlacement CellStrategy = "fused"

// cellConfig is cfg as the cell planner gets it: a fused member that runs from its changed source is one isolated subprocess.
func (c fusedChoice) cellConfig(cfg ExtConfig) ExtConfig {
	if c.rebuiltFrom != "" {
		cfg.fusedRebuiltFrom = c.rebuiltFrom
		cfg.Isolation = "isolated"
	}
	return cfg
}

// Placements of a fused member whose compiled copy runs although its configured source is not the one it was compiled from.
const (
	fusedToolchainMissingReason = "fused (source differs; toolchain missing)"
	fusedSourceUnreadableReason = "fused (source unreadable)"
)

// resolveFused decides whether cfg runs as the copy compiled into this Piglet Binary. It does unless the Binary was built with
// `build.fusedReload: source`, cfg carries a source, and that source no longer has the digest baked for the member: then the member
// runs as a subprocess built from the source, at a start and on a reload, until a `pig piglet build` fuses the edited source.
// Without a Go toolchain, or with a source that cannot be read, the compiled copy runs and the choice carries one warning.
// Callers test fusedResolver first: stock pig has none.
// pig additive (D31): a fused member whose source changed runs from its source.
func resolveFused(cfg ExtConfig) fusedChoice {
	serve, ok := fusedResolver.FusedServe(cfg)
	if !ok {
		return fusedChoice{}
	}
	sources, ok := fusedResolver.(FusedSourceResolver)
	if !ok || cfg.Source == "" {
		return fusedChoice{serve: serve}
	}
	baked, ok := sources.BakedSourceDigest(cfg.Name)
	if !ok {
		return fusedChoice{serve: serve}
	}
	digest, err := FusedSourceDigest(cfg.Source)
	if err != nil {
		return fusedChoice{serve: serve, reason: fusedSourceUnreadableReason, warning: fmt.Sprintf("source at %s cannot be read as the Go module of the fused copy: running the fused copy: %v", cfg.Source, err)}
	}
	if digest == baked {
		return fusedChoice{serve: serve}
	}
	// The builder's readiness check (ensureBuildToolchain): its message names the missing toolchain and the remedy.
	if err := missingCompiler("go"); err != nil {
		return fusedChoice{serve: serve, reason: fusedToolchainMissingReason, warning: fmt.Sprintf("source at %s differs from the fused copy; no Go toolchain: running the fused copy: %v", cfg.Source, err)}
	}
	return fusedChoice{rebuiltFrom: cfg.Source}
}
