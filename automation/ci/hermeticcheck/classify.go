// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// TargetPlatform identifies a GOOS/GOARCH pair inspected by the static classifier.
type TargetPlatform struct {
	GOOS   string
	GOARCH string
}

// DefaultPlatforms covers every OS the CI matrix tests. A package is provably
// hermetic only when it satisfies every rule across all three platforms.
var DefaultPlatforms = []TargetPlatform{
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "windows", GOARCH: "amd64"},
}

// Config controls how Analyze inspects a Go module.
type Config struct {
	Root         string
	Patterns     []string
	Platforms    []TargetPlatform
	IncludeEmpty bool // include packages with zero test files in Results
	Race         bool // -race runs are never cacheable; forces every package non-hermetic
	// CGOValues specifies the CGO_ENABLED values to inspect (e.g. ["0", "1"]).
	// When empty, Analyze inspects both CGO_ENABLED=0 and CGO_ENABLED=1 so cgo-tagged
	// files cannot slip past a cgo-enabled test runner.
	CGOValues []string
}

// Reason explains one concrete non-hermeticity finding for a package.
type Reason struct {
	Code     string `json:"code"`
	Platform string `json:"platform,omitempty"`
	Detail   string `json:"detail"`
}

// PackageResult is the fail-closed classification of one package.
type PackageResult struct {
	ImportPath      string   `json:"importPath"`
	RelPath         string   `json:"relPath"`
	HasTests        bool     `json:"hasTests"`
	Hermetic        bool     `json:"hermetic"`
	Reasons         []Reason `json:"reasons,omitempty"`
	TestFileCount   int      `json:"testFileCount"`
	EstimatedWeight float64  `json:"estimatedWeight,omitempty"`
}

// Summary aggregates partition counts and timing shares across analyzed packages.
type Summary struct {
	Module               string          `json:"module"`
	Platforms            []string        `json:"platforms"`
	TotalPackages        int             `json:"totalPackages"`
	TestedPackages       int             `json:"testedPackages"`
	HermeticPackages     int             `json:"hermeticPackages"`
	NonHermeticPackages  int             `json:"nonHermeticPackages"`
	RaceForced           bool            `json:"raceForced,omitempty"`
	KnownTimingSeconds   float64         `json:"knownTimingSeconds,omitempty"`
	HermeticTimingSec    float64         `json:"hermeticTimingSeconds,omitempty"`
	NonHermeticTimingSec float64         `json:"nonHermeticTimingSeconds,omitempty"`
	EstimatedTimeShare   float64         `json:"estimatedHermeticTimeShare"`
	TimingNotes          string          `json:"timingNotes,omitempty"`
	Packages             []PackageResult `json:"packages"`
}

// goListPackage matches the subset of `go list -test -deps -json` fields we inspect.
type goListPackage struct {
	Dir                string
	ImportPath         string
	Name               string
	Standard           bool
	ForTest            string
	Module             *goListModule
	GoFiles            []string
	CgoFiles           []string
	CompiledGoFiles    []string
	IgnoredGoFiles     []string
	TestGoFiles        []string
	XTestGoFiles       []string
	EmbedPatterns      []string
	EmbedFiles         []string
	TestEmbedPatterns  []string
	TestEmbedFiles     []string
	XTestEmbedPatterns []string
	XTestEmbedFiles    []string
	Imports            []string
	TestImports        []string
	XTestImports       []string
	Deps               []string
	Incomplete         bool
	Error              *goListError
	DepsErrors         []*goListError
}

type goListModule struct {
	Path string
	Dir  string
	Main bool
}

type goListError struct {
	Pos string
	Err string
}

// forbiddenImports are standard library and third-party packages whose presence in a
// package or its transitive test dependency closure implies non-hermetic host
// interaction (process spawning, network sockets, CGO, plugins, or raw OS IPC).
var forbiddenImports = map[string]string{
	"os/exec":                       "spawns external subprocesses whose binaries and inputs are invisible to Go test cache",
	"plugin":                        "loads dynamic shared object plugins from disk",
	"runtime/cgo":                   "links native C/cgo code outside pure-Go hermetic boundary",
	"net":                           "opens or interacts with OS network sockets/DNS",
	"net/http":                      "uses HTTP client/server transport or listeners",
	"net/http/httptest":             "binds loopback HTTP listeners or simulates network servers",
	"net/rpc":                       "opens network RPC listeners/clients",
	"net/smtp":                      "opens SMTP network connections",
	"os/signal":                     "registers process-wide OS signal handlers",
	"os/user":                       "reads host user/passwd database outside module",
	"github.com/gorilla/websocket":  "uses WebSocket network transport",
	"github.com/gofrs/flock":        "acquires OS-level cross-process file locks",
	"github.com/fsnotify/fsnotify":  "subscribes to OS kernel filesystem event watchers",
	"github.com/Microsoft/go-winio": "uses Windows named pipes / kernel IPC",
}

// forbiddenRepoImportPrefixes match in-repo helper packages that wrap external tools,
// Pi oracles, Node/Python/Rust/Lisp runtimes, tmux/PTY terminals, or network fixtures.
var forbiddenRepoImportPrefixes = []struct {
	suffix string
	code   string
	reason string
}{
	{"/internal/pioracle", "oracle", "invokes the external Pi comparator binary"},
	{"/internal/upstreammirror", "oracle", "reads external .upstream Pi mirror tree"},
	{"/internal/browsertest", "external-tool", "launches external browser/websocket harness"},
	{"/internal/npmpublish/npmtest", "external-tool", "builds/execs fake npm subprocess fixtures"},
	{"/internal/nodespawn", "external-tool", "spawns Node/child processes"},
	{"/internal/crossspawn", "external-tool", "spawns cross-platform subprocesses"},
	{"/internal/linkerexec", "external-tool", "invokes external linker/compiler processes"},
	{"/internal/childwait", "external-tool", "manages child process lifetimes"},
	{"/internal/toolchain", "external-tool", "discovers or runs external toolchain binaries"},
	{"/internal/packagemanager", "external-tool", "invokes npm/git package manager binaries"},
	{"/internal/managementhttp", "network", "serves management HTTP endpoints"},
	{"/internal/viewendpoint", "network", "binds local HTTP/socket view endpoints"},
	{"/internal/perfgate", "external-tool", "runs external performance/PTY benchmark harness"},
	{"/internal/testbudget", "test-env", "reads PIG_TEST_WAIT_TIMEOUT_SEC and process timing budgets"},
	{"/internal/testscaling", "test-env", "measures host runtime timing/memory scaling and PIG_SCALING_REPORT"},
	{"/internal/testenv", "testenv-side-effect", "imports internal/testenv (evaluated selectively; non-allowlisted symbols fail closed)"},
	{"/coding/extension/host/subprocess", "subprocess-host", "launches external Go/Node/Python/Rust/Lisp extension processes"},
	{"/test/extension-conformance/testfixture", "subprocess-host", "builds/runs external extension conformance fixture binaries"},
	{"/test/parity/runner", "oracle", "runs Pi parity comparator scenarios"},
}

// testenvHermeticSymbols is the strict allowlist of internal/testenv helpers that do
// not spawn subprocesses, bind sockets, read host-external binaries, or depend on
// ambient host environment variables. Any call to internal/testenv outside this set
// (or any indirect/blank/dot import of internal/testenv) classifies as non-hermetic.
var testenvHermeticSymbols = map[string]bool{
	"BaselineGoroutines":       true,
	"GoroutineBaseline":        true,
	"LiveGoroutines":           true,
	"LeakedGoroutines":         true,
	"RunWithoutGoroutineLeaks": true,
	"Await":                    true,
	"DirectoryLink":            true,
	"RequireDirectoryLink":     true,
	"RequireSymlink":           true,
	"Symlink":                  true,
	"ReadOnlyDir":              true,
	"GrantOthersRead":          true,
	"InheritOthersRead":        true,
}

// forbiddenSyscallSelectors flag direct process/exec/network/socket syscalls.
var forbiddenSyscallSelectors = map[string]string{
	"ForkExec":            "direct syscall.ForkExec process spawn",
	"StartProcess":        "direct syscall.StartProcess process spawn",
	"Exec":                "direct syscall.Exec process replacement",
	"Socket":              "direct raw socket creation",
	"Bind":                "direct raw socket bind",
	"Listen":              "direct raw socket listen",
	"Connect":             "direct raw socket connect",
	"Accept":              "direct raw socket accept",
	"CreateProcess":       "direct Windows process spawn",
	"CreateProcessAsUser": "direct Windows process spawn",
}

// forbiddenOSSelectors flag OS process/environment/host-external calls that make a
// test depend on untracked ambient process or filesystem state.
var forbiddenOSSelectors = map[string]string{
	"StartProcess":  "os.StartProcess launches an external process",
	"FindProcess":   "os.FindProcess inspects external host processes",
	"Getenv":        "os.Getenv reads ambient host environment outside allowlisted testenv",
	"LookupEnv":     "os.LookupEnv reads ambient host environment outside allowlisted testenv",
	"Environ":       "os.Environ reads entire ambient host environment",
	"ExpandEnv":     "os.ExpandEnv reads ambient host environment",
	"UserHomeDir":   "os.UserHomeDir reads host home directory outside module",
	"UserConfigDir": "os.UserConfigDir reads host user config directory outside module",
	"UserCacheDir":  "os.UserCacheDir reads host user cache directory outside module",
	"Hostname":      "os.Hostname depends on host identity",
	"Executable":    "os.Executable inspects binary path on host filesystem",
	"TempDir":       "os.TempDir reads shared ambient host temp directory outside per-test isolation",
}

// checkedOSReadFuncs are os filesystem read APIs whose path argument must be
// statically proven to remain within an isolated per-test scratch directory.
// Any non-direct-call reference (e.g. assigning os.ReadFile to a variable) fails closed.
var checkedOSReadFuncs = map[string]bool{
	"ReadFile": true,
	"Open":     true,
	"OpenFile": true,
	"ReadDir":  true,
	"Stat":     true,
	"Lstat":    true,
	"DirFS":    true,
}

// externalRuntimeEnvTokens detect string literals in test files that reference
// external tool binaries, Pi oracle env vars, or tmux.
var externalRuntimeEnvTokens = []string{
	"PIG_PARITY_PI_BIN",
	"PI_PACKAGE_ROOT",
	"PIG_REQUIRE_PI_ORACLE",
	"PIG_TEST_FIXTURE_EXT_BIN",
	"PIG_TEST_SDK_FIXTURE_BIN",
	"PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN",
	"PIG_TEST_RUST_SDK_FIXTURE_BIN",
	"PIG_ZSTD",
	"CARGO_TARGET_DIR",
	"TMUX",
}

// externalFixturePathTokens detect paths referencing external or out-of-package
// fixtures that Go test cache does not hash reliably across checkouts or machines.
var externalFixturePathTokens = []string{
	".upstream/",
	"node_modules/",
	"../testdata",
	"../../testdata",
	"../../../testdata",
	"/testdata/",
}

// Analyze inspects the module rooted at cfg.Root across cfg.Platforms and returns
// a deterministic, fail-closed hermeticity partition.
func Analyze(cfg Config) (*Summary, error) {
	if cfg.Root == "" {
		cfg.Root = "."
	}
	absRoot, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", cfg.Root, err)
	}
	patterns := cfg.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	platforms := cfg.Platforms
	if len(platforms) == 0 {
		platforms = DefaultPlatforms
	}

	type pkgState struct {
		importPath    string
		relPath       string
		dir           string
		hasTests      bool
		testFileCount int
		reasons       map[string]Reason
	}
	states := make(map[string]*pkgState)
	ensureState := func(importPath, dir, modPath string) *pkgState {
		st, ok := states[importPath]
		if !ok {
			rel := relImportPath(importPath, modPath)
			st = &pkgState{
				importPath: importPath,
				relPath:    rel,
				dir:        dir,
				reasons:    make(map[string]Reason),
			}
			states[importPath] = st
		}
		if st.dir == "" && dir != "" {
			st.dir = dir
		}
		return st
	}
	addReason := func(st *pkgState, code, platform, detail string) {
		key := code + "|" + platform + "|" + detail
		if _, exists := st.reasons[key]; !exists {
			st.reasons[key] = Reason{Code: code, Platform: platform, Detail: detail}
		}
	}

	var modulePath string
	platformLabels := make([]string, 0, len(platforms))

	// Cache AST scan findings per file path so files shared across OS targets are parsed once.
	fileASTCache := make(map[string][]astFinding)
	getFileFindings := func(absFile string, isTest bool, modPath string) ([]astFinding, error) {
		cacheKey := absFile
		if isTest {
			cacheKey = "test:" + absFile
		}
		if cached, ok := fileASTCache[cacheKey]; ok {
			return cached, nil
		}
		findings, err := scanGoFile(absFile, isTest, modPath)
		if err != nil {
			return nil, err
		}
		fileASTCache[cacheKey] = findings
		return findings, nil
	}

	cgoValues := cfg.CGOValues
	if len(cgoValues) == 0 {
		cgoValues = []string{"0", "1"}
	}

	for _, plat := range platforms {
		platBase := plat.GOOS + "/" + plat.GOARCH
		platformLabels = append(platformLabels, platBase)
		for _, cgoVal := range cgoValues {
			platLabel := platBase
			if cgoVal == "1" {
				platLabel = platBase + "+cgo"
			}

			pkgs, err := runGoList(absRoot, plat, cgoVal, patterns)
			if err != nil {
				return nil, fmt.Errorf("go list (%s): %w", platLabel, err)
			}

			byImport := make(map[string]*goListPackage, len(pkgs))
			// Map base package import path -> its real code/test packages (excluding the
			// synthetic `<pkg>.test` main harness generated by `go test`, which always
			// imports `testing/internal/testdeps` -> `internal/fuzz` -> `os/exec`/`os/signal`).
			targetEntries := make(map[string][]*goListPackage)
			baseTargets := make(map[string]*goListPackage)
			for _, p := range pkgs {
				byImport[p.ImportPath] = p
				if p.Module != nil && p.Module.Main && modulePath == "" {
					modulePath = p.Module.Path
				}
			}
			for _, p := range pkgs {
				if p.Module == nil || !p.Module.Main {
					continue
				}
				// Skip synthetic testmain package (<pkg>.test, Name == "main"): its generated
				// GoFiles live under GOCACHE and pull in testing/internal/testdeps.
				if strings.HasSuffix(p.ImportPath, ".test") && p.Name == "main" {
					continue
				}
				if p.ForTest != "" {
					clean := stripForTestSuffix(p.ImportPath)
					if clean == p.ForTest || clean == p.ForTest+"_test" {
						targetEntries[p.ForTest] = append(targetEntries[p.ForTest], p)
					}
					continue
				}
				baseTargets[p.ImportPath] = p
				targetEntries[p.ImportPath] = append(targetEntries[p.ImportPath], p)
			}

			directTargets, err := runGoListTargets(absRoot, plat, cgoVal, patterns)
			if err != nil {
				return nil, fmt.Errorf("go list targets (%s): %w", platLabel, err)
			}

			for _, imp := range directTargets {
				basePkg := baseTargets[imp]
				dir := ""
				if basePkg != nil {
					dir = basePkg.Dir
				}
				st := ensureState(imp, dir, modulePath)
				entries := targetEntries[imp]
				if len(entries) == 0 {
					addReason(st, "load-error", platLabel, "package metadata missing from go list -test -deps (fail-closed)")
					continue
				}

				visitedDeps := make(map[string]bool)
				hasTestsOnPlatform := false
				testFilesSet := make(map[string]bool)

				for _, entry := range entries {
					if entry.Error != nil {
						addReason(st, "load-error", platLabel, "go list error: "+entry.Error.Err)
					}
					for _, de := range entry.DepsErrors {
						if de != nil {
							addReason(st, "load-error", platLabel, "dependency load error: "+de.Err)
						}
					}
					if entry.Incomplete {
						addReason(st, "load-error", platLabel, "incomplete package metadata (fail-closed)")
					}
					if len(entry.CgoFiles) > 0 {
						addReason(st, "cgo", platLabel, fmt.Sprintf("package contains %d cgo file(s): %s", len(entry.CgoFiles), strings.Join(entry.CgoFiles, ", ")))
					}
					if len(entry.TestGoFiles) > 0 || len(entry.XTestGoFiles) > 0 {
						hasTestsOnPlatform = true
						for _, f := range entry.TestGoFiles {
							testFilesSet[f] = true
						}
						for _, f := range entry.XTestGoFiles {
							testFilesSet[f] = true
						}
					}
					if entry.ForTest == imp && strings.HasSuffix(stripForTestSuffix(entry.ImportPath), "_test") && len(entry.GoFiles) > 0 {
						hasTestsOnPlatform = true
						for _, f := range entry.GoFiles {
							testFilesSet[f] = true
						}
					}

					checkEmbeds := func(embedPatterns, files []string, scope string) {
						for _, pat := range embedPatterns {
							if strings.Contains(pat, "..") {
								addReason(st, "external-embed", platLabel, fmt.Sprintf("%s //go:embed pattern %q traverses parent directory", scope, pat))
							}
						}
						for _, f := range files {
							slash := filepath.ToSlash(f)
							if strings.HasPrefix(slash, "testdata/") || strings.Contains(slash, "/testdata/") || isExternalEmbedArtifact(slash) {
								addReason(st, "external-embed", platLabel, fmt.Sprintf("%s embeds fixture/runtime artifact %q", scope, slash))
							}
						}
					}
					checkEmbeds(entry.EmbedPatterns, entry.EmbedFiles, "package")
					checkEmbeds(entry.TestEmbedPatterns, entry.TestEmbedFiles, "test")
					checkEmbeds(entry.XTestEmbedPatterns, entry.XTestEmbedFiles, "xtest")

					for _, d := range entry.Imports {
						visitedDeps[d] = true
					}
					for _, d := range entry.TestImports {
						visitedDeps[d] = true
					}
					for _, d := range entry.XTestImports {
						visitedDeps[d] = true
					}
					for _, d := range entry.Deps {
						visitedDeps[d] = true
					}
				}

				if hasTestsOnPlatform {
					st.hasTests = true
					if len(testFilesSet) > st.testFileCount {
						st.testFileCount = len(testFilesSet)
					}
				}

				for dep := range visitedDeps {
					cleanDep := stripForTestSuffix(dep)
					if reason, bad := forbiddenImports[cleanDep]; bad {
						if !isOnlyViaAllowlistedTestenv(cleanDep, entries, byImport, modulePath) {
							addReason(st, "import:"+cleanDep, platLabel, fmt.Sprintf("imports %s (%s)", cleanDep, reason))
						}
					}
					if depPkg := byImport[dep]; depPkg != nil {
						if len(depPkg.CgoFiles) > 0 {
							addReason(st, "cgo", platLabel, fmt.Sprintf("transitive dependency %s uses cgo (%s)", cleanDep, strings.Join(depPkg.CgoFiles, ", ")))
						}
						if depPkg.Incomplete || depPkg.Error != nil {
							addReason(st, "load-error", platLabel, fmt.Sprintf("dependency %s has incomplete/error metadata (fail-closed)", cleanDep))
						}
					}
					if modulePath != "" && strings.HasPrefix(cleanDep, modulePath+"/") {
						for _, rule := range forbiddenRepoImportPrefixes {
							if cleanDep == modulePath+rule.suffix || strings.HasPrefix(cleanDep, modulePath+rule.suffix+"/") {
								if rule.suffix == "/internal/testenv" {
									if !isDirectTestImport(entries, modulePath+"/internal/testenv") {
										addReason(st, rule.code, platLabel, fmt.Sprintf("transitively imports %s", cleanDep))
									}
								} else {
									addReason(st, rule.code, platLabel, fmt.Sprintf("imports %s (%s)", cleanDep, rule.reason))
								}
							}
						}
					}
				}

				checkedFiles := make(map[string]bool)
				scanPkgFiles := func(p *goListPackage, isTarget bool) {
					if p == nil || p.Dir == "" {
						return
					}
					checkList := func(files []string, isTest bool) {
						for _, name := range files {
							abs := name
							if !filepath.IsAbs(abs) {
								abs = filepath.Join(p.Dir, name)
							}
							if checkedFiles[abs] {
								continue
							}
							checkedFiles[abs] = true
							fileIsTest := isTest || strings.HasSuffix(name, "_test.go")
							findings, scanErr := getFileFindings(abs, fileIsTest, modulePath)
							if scanErr != nil {
								addReason(st, "parse-error", platLabel, fmt.Sprintf("parse %s: %v (fail-closed)", name, scanErr))
								continue
							}
							for _, f := range findings {
								prefix := ""
								if !isTarget {
									prefix = fmt.Sprintf("via %s: ", relImportPath(stripForTestSuffix(p.ImportPath), modulePath))
								}
								addReason(st, f.code, platLabel, prefix+f.detail)
							}
						}
					}
					checkList(p.GoFiles, false)
					if isTarget {
						checkList(p.TestGoFiles, true)
						checkList(p.XTestGoFiles, true)
					}
				}

				for _, entry := range entries {
					scanPkgFiles(entry, true)
				}
				directTestenv := modulePath != "" && isDirectTestImport(entries, modulePath+"/internal/testenv")
				for dep := range visitedDeps {
					cleanDep := stripForTestSuffix(dep)
					if modulePath != "" && cleanDep == modulePath+"/internal/testenv" {
						continue
					}
					depPkg := byImport[dep]
					if depPkg == nil {
						depPkg = byImport[cleanDep]
					}
					if depPkg != nil && depPkg.Standard {
						continue
					}
					if directTestenv && isOnlyViaAllowlistedTestenv(cleanDep, entries, byImport, modulePath) {
						continue
					}
					if depPkg == nil {
						if cleanDep == "C" {
							continue
						}
						addReason(st, "unverified-dep", platLabel, fmt.Sprintf("non-standard dependency %s metadata missing from go list (fail-closed)", cleanDep))
						continue
					}
					if depPkg.ForTest != "" && stripForTestSuffix(depPkg.ImportPath) == imp {
						continue
					}
					if depPkg.Dir == "" {
						addReason(st, "unverified-dep", platLabel, fmt.Sprintf("non-standard dependency %s source directory missing (fail-closed)", cleanDep))
						continue
					}
					scanPkgFiles(depPkg, false)
				}

				// Fail closed on any package with an on-disk testdata/ directory: Go's test cache
				// hashes opened module files via mtime+size (cmd/go/internal/test/test.go hashOpen)
				// rather than content hashes, which cannot be safely replayed across remote
				// runners with differing checkout timestamps (e.g. git clone / actions/checkout).
				if st.dir != "" {
					if tdReasons := inspectTestdataDir(st.dir); len(tdReasons) > 0 {
						for _, r := range tdReasons {
							addReason(st, r.Code, platLabel, r.Detail)
						}
					}
				}
			}
		}
	}

	if cfg.Race {
		for _, st := range states {
			addReason(st, "race", "all", "-race runs always enforce -count=1")
		}
	}

	knownTimings := loadKnownPackageTimings(absRoot, modulePath)

	var results []PackageResult
	var totalPkgs, testedPkgs, hermeticPkgs, nonHermeticPkgs int
	var knownTotalSec, hermeticKnownSec, nonHermeticKnownSec float64

	var sortedImports []string
	for imp := range states {
		sortedImports = append(sortedImports, imp)
	}
	sort.Strings(sortedImports)

	for _, imp := range sortedImports {
		st := states[imp]
		totalPkgs++
		if !st.hasTests && !cfg.IncludeEmpty {
			continue
		}
		if st.hasTests {
			testedPkgs++
		}
		reasons := make([]Reason, 0, len(st.reasons))
		for _, r := range st.reasons {
			reasons = append(reasons, r)
		}
		sort.Slice(reasons, func(i, j int) bool {
			if reasons[i].Code != reasons[j].Code {
				return reasons[i].Code < reasons[j].Code
			}
			if reasons[i].Platform != reasons[j].Platform {
				return reasons[i].Platform < reasons[j].Platform
			}
			return reasons[i].Detail < reasons[j].Detail
		})
		hermetic := len(reasons) == 0 && !cfg.Race
		if st.hasTests {
			if hermetic {
				hermeticPkgs++
			} else {
				nonHermeticPkgs++
			}
		}
		weight := knownTimings[imp]
		if st.hasTests && weight > 0 {
			knownTotalSec += weight
			if hermetic {
				hermeticKnownSec += weight
			} else {
				nonHermeticKnownSec += weight
			}
		}
		results = append(results, PackageResult{
			ImportPath:      st.importPath,
			RelPath:         st.relPath,
			HasTests:        st.hasTests,
			Hermetic:        hermetic,
			Reasons:         reasons,
			TestFileCount:   st.testFileCount,
			EstimatedWeight: weight,
		})
	}

	var estHermeticWeight, estTotalWeight float64
	for _, r := range results {
		if !r.HasTests {
			continue
		}
		w := r.EstimatedWeight
		if w <= 0 {
			w = float64(max(r.TestFileCount, 1))
		}
		estTotalWeight += w
		if r.Hermetic {
			estHermeticWeight += w
		}
	}
	var share float64
	if estTotalWeight > 0 {
		share = estHermeticWeight / estTotalWeight
	}

	notes := "Sharded packages use measured wall seconds from automation/ci/test-shard-timings/*.json; remaining packages use test-file count proxy (1s/file)."
	if knownTotalSec == 0 {
		notes = "No shard timing JSON files found; estimated share uses test-file count proxy."
	}

	return &Summary{
		Module:               modulePath,
		Platforms:            platformLabels,
		TotalPackages:        totalPkgs,
		TestedPackages:       testedPkgs,
		HermeticPackages:     hermeticPkgs,
		NonHermeticPackages:  nonHermeticPkgs,
		RaceForced:           cfg.Race,
		KnownTimingSeconds:   knownTotalSec,
		HermeticTimingSec:    hermeticKnownSec,
		NonHermeticTimingSec: nonHermeticKnownSec,
		EstimatedTimeShare:   share,
		TimingNotes:          notes,
		Packages:             results,
	}, nil
}

func runGoList(root string, plat TargetPlatform, cgoEnabled string, patterns []string) ([]*goListPackage, error) {
	args := append([]string{"list", "-e", "-test", "-deps", "-json"}, patterns...)
	cmd := exec.Command("go", args...) //nolint:gosec // invokes the local Go toolchain with fixed flags and user-selected package patterns
	cmd.Dir = root
	cmd.Env = platformEnv(plat, cgoEnabled)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	dec := json.NewDecoder(&stdout)
	var pkgs []*goListPackage
	for {
		var p goListPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode go list json: %w", err)
		}
		pkgs = append(pkgs, &p)
	}
	return pkgs, nil
}

func runGoListTargets(root string, plat TargetPlatform, cgoEnabled string, patterns []string) ([]string, error) {
	args := append([]string{"list", "-e", "-f", "{{.ImportPath}}"}, patterns...)
	cmd := exec.Command("go", args...) //nolint:gosec // invokes the local Go toolchain with fixed flags and user-selected package patterns
	cmd.Dir = root
	cmd.Env = platformEnv(plat, cgoEnabled)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func platformEnv(plat TargetPlatform, cgoEnabled string) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+3)
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOOS=") || strings.HasPrefix(kv, "GOARCH=") || strings.HasPrefix(kv, "CGO_ENABLED=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	if cgoEnabled == "" {
		cgoEnabled = "0"
	}
	filtered = append(filtered,
		"GOOS="+plat.GOOS,
		"GOARCH="+plat.GOARCH,
		"CGO_ENABLED="+cgoEnabled,
	)
	return filtered
}

func stripForTestSuffix(importPath string) string {
	if before, _, ok := strings.Cut(importPath, " ["); ok {
		return before
	}
	return importPath
}

func relImportPath(importPath, modulePath string) string {
	if importPath == modulePath {
		return "."
	}
	if modulePath != "" && strings.HasPrefix(importPath, modulePath+"/") {
		return "./" + strings.TrimPrefix(importPath, modulePath+"/")
	}
	return importPath
}

func isDirectTestImport(entries []*goListPackage, want string) bool {
	for _, e := range entries {
		if slices.Contains(e.TestImports, want) || slices.Contains(e.XTestImports, want) {
			return true
		}
		if e.ForTest != "" && strings.HasSuffix(stripForTestSuffix(e.ImportPath), "_test") && slices.Contains(e.Imports, want) {
			return true
		}
	}
	return false
}

// isOnlyViaAllowlistedTestenv returns true if forbiddenDep is reachable from entries
// ONLY by traversing through <modulePath>/internal/testenv.
func isOnlyViaAllowlistedTestenv(forbiddenDep string, entries []*goListPackage, byImport map[string]*goListPackage, modulePath string) bool {
	if modulePath == "" {
		return false
	}
	testenvPath := modulePath + "/internal/testenv"
	visited := make(map[string]bool)
	var queue []string
	enqueue := func(imp string) {
		clean := stripForTestSuffix(imp)
		if clean == testenvPath {
			return
		}
		if !visited[imp] {
			visited[imp] = true
			queue = append(queue, imp)
		}
	}
	for _, e := range entries {
		for _, imp := range e.Imports {
			enqueue(imp)
		}
		for _, imp := range e.TestImports {
			enqueue(imp)
		}
		for _, imp := range e.XTestImports {
			enqueue(imp)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		clean := stripForTestSuffix(cur)
		if clean == forbiddenDep {
			return false
		}
		pkg := byImport[cur]
		if pkg == nil {
			pkg = byImport[clean]
		}
		if pkg == nil {
			continue
		}
		for _, next := range pkg.Imports {
			enqueue(next)
		}
	}
	return true
}

type astFinding struct {
	code   string
	detail string
}

// scanGoFile parses a single Go source file and reports fail-closed non-hermeticity findings.
func scanGoFile(absFile string, isTest bool, modulePath string) ([]astFinding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absFile, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var findings []astFinding
	baseName := filepath.Base(absFile)
	add := func(code, detail string) {
		findings = append(findings, astFinding{code: code, detail: baseName + ": " + detail})
	}

	aliases := make(map[string]string)
	testenvPath := ""
	if modulePath != "" {
		testenvPath = modulePath + "/internal/testenv"
	}

	for _, imp := range file.Imports {
		impPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			add("parse-error", "invalid import literal "+imp.Path.Value)
			continue
		}
		if impPath == "C" {
			add("cgo", `imports "C"`)
			continue
		}
		localName := defaultImportName(impPath)
		if imp.Name != nil {
			localName = imp.Name.Name
		}
		if impPath == testenvPath && (localName == "." || localName == "_") {
			add("testenv-side-effect", "dot or blank import of internal/testenv cannot be statically verified (fail-closed)")
		}
		if localName != "_" && localName != "." {
			aliases[localName] = impPath
		} else if localName == "." {
			if impPath == "os" || impPath == "syscall" || impPath == "golang.org/x/sys/unix" || impPath == "golang.org/x/sys/windows" {
				add("dot-import", fmt.Sprintf("dot import of %q hides side-effect call sites (fail-closed)", impPath))
			}
		}
	}

	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:linkname ") {
				if strings.Contains(c.Text, "syscall.") || strings.Contains(c.Text, "os.") {
					add("linkname", "uses //go:linkname into os/syscall")
				}
			}
		}
	}

	// Track whether this file (test or production code linked into tests) performs
	// filesystem reads/walks/globs/stats outside a proven per-test scratch directory.
	// Any filepath.Walk/WalkDir/Glob or parent-traversal ("..") argument is fail-closed
	// because Go's test cache hashes module files by mtime+size (not content) and misses
	// newly created files in sibling directories or outside the module.
	var hasFileIORead bool
	var hasParentRef bool
	var parentRefSample string
	// Collect package-level constant/var string literal bindings so
	// `const ext = "/var/tmp/fixture"; os.ReadFile(ext)` is caught across functions.
	filePathBindings := make(map[string]string)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i := 0; i < len(vs.Names) && i < len(vs.Values); i++ {
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil {
						filePathBindings[vs.Names[i].Name] = v
					}
				}
			}
		}
	}
	// Validate filesystem reads per function scope in both test and production files:
	// only direct calls whose path expression is proven to stay within a
	// testing.TB.TempDir() / os.MkdirTemp("", ...) / os.CreateTemp("", ...) scratch
	// root are allowed. Record validated direct-call selectors so any aliased or
	// non-direct reference to a checked read API (e.g. `read := os.ReadFile`) is
	// rejected fail-closed below.
	directReadCallSels := make(map[*ast.SelectorExpr]bool)
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		scanFuncReads(fd, aliases, filePathBindings, directReadCallSels, add, &hasFileIORead)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			impPath := aliases[ident.Name]
			fn := sel.Sel.Name
			if impPath == "path/filepath" {
				switch fn {
				case "Walk", "WalkDir", "Glob":
					add("repo-tree-walk", fmt.Sprintf("calls filepath.%s (directory walks depend on untracked sibling/repo files)", fn))
				case "Join":
					for _, arg := range node.Args {
						if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if v, err := strconv.Unquote(lit.Value); err == nil && (v == ".." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../")) {
								hasParentRef = true
								if parentRefSample == "" {
									parentRefSample = v
								}
							}
						}
					}
				}
			}

		case *ast.SelectorExpr:
			ident, ok := node.X.(*ast.Ident)
			if !ok {
				return true
			}
			impPath, known := aliases[ident.Name]
			if !known {
				return true
			}
			sel := node.Sel.Name
			switch impPath {
			case "os":
				if reason, bad := forbiddenOSSelectors[sel]; bad {
					add("os-side-effect", fmt.Sprintf("calls os.%s (%s)", sel, reason))
				}
				if checkedOSReadFuncs[sel] && !directReadCallSels[node] {
					add("external-path-read", fmt.Sprintf("references os.%s outside a verified direct call (path cannot be statically verified; fail-closed)", sel))
				}
			case "syscall", "golang.org/x/sys/unix", "golang.org/x/sys/windows":
				if reason, bad := forbiddenSyscallSelectors[sel]; bad {
					add("syscall-side-effect", fmt.Sprintf("references %s.%s (%s)", impPath, sel, reason))
				}
			default:
				if testenvPath != "" && impPath == testenvPath {
					if !testenvHermeticSymbols[sel] {
						add("testenv-side-effect", fmt.Sprintf("uses non-allowlisted testenv.%s", sel))
					}
				}
			}

		case *ast.BasicLit:
			if !isTest || node.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(node.Value)
			if err != nil {
				return true
			}
			for _, tok := range externalRuntimeEnvTokens {
				if val == tok || strings.Contains(val, tok) {
					add("external-env-ref", fmt.Sprintf("references external tool/oracle token %q", tok))
				}
			}
			for _, tok := range externalFixturePathTokens {
				if strings.Contains(val, tok) {
					add("external-fixture", fmt.Sprintf("references external/parent fixture path %q", val))
				}
			}
		}
		return true
	})

	if hasFileIORead && hasParentRef {
		add("external-fixture", fmt.Sprintf("performs filesystem reads combined with parent-directory traversal (%q)", parentRefSample))
	}

	return findings, nil
}

// isTestingTBType reports whether expr names a testing.T, testing.B, testing.F,
// or testing.TB type (pointer or value) imported from the standard "testing" package.
func isTestingTBType(expr ast.Expr, aliases map[string]string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || aliases[id.Name] != "testing" {
		return false
	}
	switch sel.Sel.Name {
	case "T", "B", "F", "TB":
		return true
	}
	return false
}

// collectTestingTBParams returns the set of parameter names in ft whose declared
// type is testing.TB / *testing.T / *testing.B / *testing.F.
func collectTestingTBParams(ft *ast.FuncType, aliases map[string]string) map[string]bool {
	tb := make(map[string]bool)
	if ft == nil || ft.Params == nil {
		return tb
	}
	for _, field := range ft.Params.List {
		if !isTestingTBType(field.Type, aliases) {
			continue
		}
		for _, name := range field.Names {
			tb[name.Name] = true
		}
	}
	return tb
}

// isScratchDirCall reports whether call is a verified per-test isolated scratch
// directory constructor:
//   - tb.TempDir() where tb is a declared testing.TB/*testing.T/*testing.B/*testing.F
//   - os.MkdirTemp("", ...) or os.MkdirTemp(scratchVar, ...)
//   - os.CreateTemp("", ...) or os.CreateTemp(scratchVar, ...)
//
// Calls like os.TempDir() or arbitraryReceiver.TempDir() return false (fail-closed).
func isScratchDirCall(call *ast.CallExpr, aliases map[string]string, tbVars, scratchVars map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	fn := sel.Sel.Name
	if fn == "TempDir" {
		// Reject os.TempDir() and any receiver not statically known to be testing.TB/T/B/F.
		if aliases[ident.Name] != "" {
			return false
		}
		return tbVars[ident.Name]
	}
	if aliases[ident.Name] == "os" && (fn == "MkdirTemp" || fn == "CreateTemp") {
		if len(call.Args) == 0 {
			return false
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil && v == "" {
				return true
			}
		}
		return isScratchDerivedExpr(call.Args[0], aliases, tbVars, scratchVars)
	}
	return false
}

// isSafeRelativeChildExpr reports whether expr is statically proven to be a
// relative path segment (or nested child path) that cannot traverse upward ("..")
// or reset to an absolute/drive-qualified path when joined onto a scratch root.
func isSafeRelativeChildExpr(expr ast.Expr) bool {
	if p, ok := expr.(*ast.ParenExpr); ok {
		return isSafeRelativeChildExpr(p.X)
	}
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	if isAbsolutePathLiteral(v) {
		return false
	}
	// Reject Windows drive-relative paths such as "C:foo".
	if len(v) >= 2 && ((v[0] >= 'A' && v[0] <= 'Z') || (v[0] >= 'a' && v[0] <= 'z')) && v[1] == ':' {
		return false
	}
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

// isScratchDerivedExpr reports whether expr is statically proven to stay within
// a per-test scratch directory (e.g. t.TempDir(), filepath.Join(dir, "x"),
// filepath.EvalSymlinks(t.TempDir()), f.Name(), or a variable whose every
// reaching assignment is proven scratch-contained).
// Transforms that can escape the scratch root (such as filepath.Dir or
// filepath.Join with unverified/parent segments) return false.
func isScratchDerivedExpr(expr ast.Expr, aliases map[string]string, tbVars, scratchVars map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return isScratchDerivedExpr(e.X, aliases, tbVars, scratchVars)
	case *ast.Ident:
		return scratchVars[e.Name]
	case *ast.CallExpr:
		if isScratchDirCall(e, aliases, tbVars, scratchVars) {
			return true
		}
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		// Handle scratchFile.Name() from os.CreateTemp.
		if sel.Sel.Name == "Name" && len(e.Args) == 0 {
			return isScratchDerivedExpr(sel.X, aliases, tbVars, scratchVars)
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return false
		}
		imp := aliases[id.Name]
		if imp == "path/filepath" || imp == "path" {
			switch sel.Sel.Name {
			case "Clean", "Abs", "EvalSymlinks", "ToSlash", "FromSlash":
				if len(e.Args) == 1 && isScratchDerivedExpr(e.Args[0], aliases, tbVars, scratchVars) {
					return true
				}
			case "Join":
				if len(e.Args) >= 1 && isScratchDerivedExpr(e.Args[0], aliases, tbVars, scratchVars) {
					for _, seg := range e.Args[1:] {
						if !isSafeRelativeChildExpr(seg) {
							return false
						}
					}
					return true
				}
			}
		}
		return false
	}
	return false
}

// collectFuncParamAndResultNames records all parameter and named-result identifiers
// declared on ft into dst.
func collectFuncParamAndResultNames(ft *ast.FuncType, dst map[string]bool) {
	if ft == nil {
		return
	}
	for _, list := range []*ast.FieldList{ft.Params, ft.Results} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			for _, name := range field.Names {
				if name.Name != "_" {
					dst[name.Name] = true
				}
			}
		}
	}
}

// scanFuncReads validates every os.{ReadFile,Open,OpenFile,ReadDir,Stat,Lstat,DirFS}
// call inside fd (and any nested func literals such as t.Run subtests). Only direct
// calls whose path argument is proven to stay within a testing.TB.TempDir() or
// os.MkdirTemp/os.CreateTemp scratch directory within the enclosing function are
// permitted; all other reads fail closed as external-path-read or external-fixture.
func scanFuncReads(fd *ast.FuncDecl, aliases map[string]string, filePathBindings map[string]string, directReadCallSels map[*ast.SelectorExpr]bool, add func(string, string), hasFileIORead *bool) {
	tbVars := collectTestingTBParams(fd.Type, aliases)
	localPathBindings := make(map[string]string, len(filePathBindings))
	maps.Copy(localPathBindings, filePathBindings)

	// taintedVars tracks any variable whose value may come from a non-scratch source
	// (parameters, named results, range variables, address-taken variables, bare
	// zero-value declarations, compound assignments, or secondary multi-return values).
	taintedVars := make(map[string]bool)
	collectFuncParamAndResultNames(fd.Type, taintedVars)
	if fd.Recv != nil {
		for _, field := range fd.Recv.List {
			for _, name := range field.Names {
				if name.Name != "_" {
					taintedVars[name.Name] = true
				}
			}
		}
	}

	// Also collect testing.TB params (and taint non-scratch params) from nested func
	// literals (e.g. t.Run("x", func(t *testing.T) { ... })).
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok {
			for name := range collectTestingTBParams(fl.Type, aliases) {
				tbVars[name] = true
			}
			collectFuncParamAndResultNames(fl.Type, taintedVars)
		}
		return true
	})

	// Collect all assignments per variable name so that a variable is considered
	// scratch-derived ONLY if EVERY assignment to it in fd.Body is proven
	// scratch-contained (invalidating scratch provenance on any non-scratch reassignment).
	varAssigns := make(map[string][]ast.Expr)
	recordAssign := func(lhs, rhs ast.Expr) {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Name == "_" {
			return
		}
		if rhs == nil {
			taintedVars[id.Name] = true
			return
		}
		varAssigns[id.Name] = append(varAssigns[id.Name], rhs)
	}
	taintExpr := func(expr ast.Expr) {
		if id, ok := expr.(*ast.Ident); ok && id.Name != "_" {
			taintedVars[id.Name] = true
		}
	}

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.UnaryExpr:
			if stmt.Op == token.AND {
				taintExpr(stmt.X)
			}
		case *ast.RangeStmt:
			taintExpr(stmt.Key)
			taintExpr(stmt.Value)
		case *ast.AssignStmt:
			if stmt.Tok != token.DEFINE && stmt.Tok != token.ASSIGN {
				for _, lhs := range stmt.Lhs {
					taintExpr(lhs)
				}
				return true
			}
			// Handle multi-return assignments like `dir, err := os.MkdirTemp("", "p")`
			// or `root, err := filepath.EvalSymlinks(t.TempDir())`.
			if len(stmt.Rhs) == 1 && len(stmt.Lhs) > 1 {
				recordAssign(stmt.Lhs[0], stmt.Rhs[0])
				for _, extra := range stmt.Lhs[1:] {
					taintExpr(extra)
				}
				return true
			}
			for i := 0; i < len(stmt.Lhs); i++ {
				if i < len(stmt.Rhs) {
					if id, ok := stmt.Lhs[i].(*ast.Ident); ok {
						if lit, ok := stmt.Rhs[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if v, err := strconv.Unquote(lit.Value); err == nil {
								localPathBindings[id.Name] = v
							}
						}
					}
					recordAssign(stmt.Lhs[i], stmt.Rhs[i])
				} else {
					taintExpr(stmt.Lhs[i])
				}
			}
		case *ast.ValueSpec:
			if len(stmt.Values) == 0 {
				for _, name := range stmt.Names {
					taintExpr(name)
				}
				return true
			}
			if len(stmt.Values) == 1 && len(stmt.Names) > 1 {
				recordAssign(stmt.Names[0], stmt.Values[0])
				for _, extra := range stmt.Names[1:] {
					taintExpr(extra)
				}
				return true
			}
			for i, name := range stmt.Names {
				if i < len(stmt.Values) {
					if lit, ok := stmt.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							localPathBindings[name.Name] = v
						}
					}
					recordAssign(name, stmt.Values[i])
				} else {
					taintExpr(name)
				}
			}
		}
		return true
	})

	// If a testing.TB parameter name is reassigned or shadowed in the function body,
	// do not trust it as a testing.TB receiver.
	for name := range varAssigns {
		delete(tbVars, name)
	}

	scratchVars := make(map[string]bool)
	for changed := true; changed; {
		changed = false
		for name, rhss := range varAssigns {
			if scratchVars[name] || taintedVars[name] || len(rhss) == 0 {
				continue
			}
			allScratch := true
			for _, rhs := range rhss {
				if !isScratchDerivedExpr(rhs, aliases, tbVars, scratchVars) {
					allScratch = false
					break
				}
			}
			if allScratch {
				scratchVars[name] = true
				changed = true
			}
		}
	}

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if aliases[ident.Name] != "os" {
			return true
		}
		fn := sel.Sel.Name
		if checkedOSReadFuncs[fn] {
			directReadCallSels[sel] = true
			*hasFileIORead = true
			if len(call.Args) > 0 {
				classifyReadPathArg(call.Args[0], fn, aliases, tbVars, scratchVars, localPathBindings, add)
			}
		}
		return true
	})
}

func classifyReadPathArg(arg ast.Expr, fn string, aliases map[string]string, tbVars, scratchVars map[string]bool, pathBindings map[string]string, add func(string, string)) {
	checkLiteral := func(v string) {
		if v == ".." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../") || strings.HasPrefix(v, "..\\") || strings.Contains(v, "\\..\\") {
			add("external-fixture", fmt.Sprintf("reads parent-directory path %q via os.%s", v, fn))
		} else if isAbsolutePathLiteral(v) {
			add("external-path-read", fmt.Sprintf("reads absolute path %q via os.%s (untracked by Go test cache)", v, fn))
		} else {
			add("external-path-read", fmt.Sprintf("reads literal path %q via os.%s (outside scratch directory)", v, fn))
		}
	}
	if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if v, err := strconv.Unquote(lit.Value); err == nil {
			checkLiteral(v)
		}
		return
	}
	if id, ok := arg.(*ast.Ident); ok {
		if v, bound := pathBindings[id.Name]; bound {
			checkLiteral(v)
			return
		}
	}
	if pathLit, ok := firstAbsoluteLiteralArg(arg); ok {
		add("external-path-read", fmt.Sprintf("reads absolute path %q via os.%s (untracked by Go test cache)", pathLit, fn))
		return
	}
	if isScratchDerivedExpr(arg, aliases, tbVars, scratchVars) {
		return
	}
	add("external-path-read", fmt.Sprintf("performs filesystem read via os.%s on path not proven to stay within t.TempDir/os.MkdirTemp", fn))
}

func isAbsolutePathLiteral(v string) bool {
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "\\") {
		return true
	}
	// Windows drive-letter absolute path like C:\ or C:/
	if len(v) >= 3 && ((v[0] >= 65 && v[0] <= 90) || (v[0] >= 97 && v[0] <= 122)) && v[1] == 58 && (v[2] == 47 || v[2] == 92) {
		return true
	}
	return false
}

func firstAbsoluteLiteralArg(expr ast.Expr) (string, bool) {
	var found string
	ast.Inspect(expr, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil && isAbsolutePathLiteral(v) {
			found = v
			return false
		}
		return true
	})
	return found, found != ""
}

func defaultImportName(impPath string) string {
	base := impPath
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
		base = base[idx+1:]
	}
	if len(base) >= 2 && base[0] == 'v' && base[1] >= '0' && base[1] <= '9' {
		trimmed := impPath[:strings.LastIndexByte(impPath, '/')]
		if idx := strings.LastIndexByte(trimmed, '/'); idx >= 0 {
			return trimmed[idx+1:]
		}
		return trimmed
	}
	return base
}

func isExternalEmbedArtifact(slashPath string) bool {
	for _, ext := range []string{".pack", ".wasm", ".wasm.zst", ".bin", ".exe", ".so", ".dylib", ".dll", ".zst"} {
		if strings.HasSuffix(slashPath, ext) {
			return true
		}
	}
	return false
}

// inspectTestdataDir fails closed whenever a package carries a testdata/ directory
// on disk (or a testdata symlink): Go's test cache tracks module files via mtime+size
// rather than content digest, and symlinks or nested fixtures escape Go's cache tracking.
func inspectTestdataDir(pkgDir string) []Reason {
	td := filepath.Join(pkgDir, "testdata")
	info, err := os.Lstat(td)
	if err != nil {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return []Reason{{
			Code:   "external-testdata",
			Detail: "testdata is a symlink (outside Go test cache tracking)",
		}}
	}
	if info.IsDir() {
		return []Reason{{
			Code:   "external-testdata",
			Detail: "package has on-disk testdata/ directory (Go test cache uses mtime+size, not content hash; fail-closed for remote cache)",
		}}
	}
	return nil
}

// loadKnownPackageTimings reads automation/ci/test-shard-timings/*.json (if present)
// and sums each sharded package's recorded shard wall seconds.
func loadKnownPackageTimings(root, modulePath string) map[string]float64 {
	out := make(map[string]float64)
	dir := filepath.Join(root, "automation", "ci", "test-shard-timings")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	type timingFile struct {
		Package string             `json:"package"`
		Shards  map[string]float64 `json:"shards"`
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.Contains(e.Name(), "windows") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var tf timingFile
		if err := json.Unmarshal(data, &tf); err != nil || tf.Package == "" {
			continue
		}
		var sum float64
		for _, sec := range tf.Shards {
			sum += sec
		}
		imp := tf.Package
		if rel, ok := strings.CutPrefix(imp, "./"); ok && modulePath != "" {
			imp = modulePath + "/" + rel
		}
		if sum > out[imp] {
			out[imp] = sum
		}
	}
	return out
}
