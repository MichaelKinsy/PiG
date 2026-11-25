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

	for _, plat := range platforms {
		platLabel := plat.GOOS + "/" + plat.GOARCH
		platformLabels = append(platformLabels, platLabel)

		pkgs, err := runGoList(absRoot, plat, patterns)
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

		directTargets, err := runGoListTargets(absRoot, plat, patterns)
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
			for dep := range visitedDeps {
				cleanDep := stripForTestSuffix(dep)
				if modulePath == "" || !strings.HasPrefix(cleanDep, modulePath+"/") {
					continue
				}
				if cleanDep == modulePath+"/internal/testenv" {
					continue
				}
				if depPkg := byImport[dep]; depPkg != nil && depPkg.Module != nil && depPkg.Module.Main {
					if depPkg.ForTest != "" && stripForTestSuffix(depPkg.ImportPath) == imp {
						continue
					}
					scanPkgFiles(depPkg, false)
				}
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

func runGoList(root string, plat TargetPlatform, patterns []string) ([]*goListPackage, error) {
	args := append([]string{"list", "-e", "-test", "-deps", "-json"}, patterns...)
	cmd := exec.Command("go", args...) //nolint:gosec // invokes the local Go toolchain with fixed flags and user-selected package patterns
	cmd.Dir = root
	cmd.Env = platformEnv(plat)
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

func runGoListTargets(root string, plat TargetPlatform, patterns []string) ([]string, error) {
	args := append([]string{"list", "-e", "-f", "{{.ImportPath}}"}, patterns...)
	cmd := exec.Command("go", args...) //nolint:gosec // invokes the local Go toolchain with fixed flags and user-selected package patterns
	cmd.Dir = root
	cmd.Env = platformEnv(plat)
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

func platformEnv(plat TargetPlatform) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+3)
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOOS=") || strings.HasPrefix(kv, "GOARCH=") || strings.HasPrefix(kv, "CGO_ENABLED=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered,
		"GOOS="+plat.GOOS,
		"GOARCH="+plat.GOARCH,
		"CGO_ENABLED=0",
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

	// Track whether this test file performs filesystem reads/walks/globs/stats directly
	// on repository paths outside t.TempDir(). Any filepath.Walk/WalkDir/Glob or
	// parent-traversal ("..") argument in a path call inside a test file is fail-closed
	// because Go's test cache hashes module files by mtime+size (not content) and misses
	// newly created files in sibling directories.
	var hasFileIORead bool
	var hasParentRef bool
	var parentRefSample string

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if !isTest {
				return true
			}
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
					add("repo-tree-walk", fmt.Sprintf("test calls filepath.%s (directory walks depend on untracked sibling/repo files)", fn))
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
			if impPath == "os" {
				switch fn {
				case "ReadFile", "Open", "OpenFile", "ReadDir", "Stat", "Lstat":
					hasFileIORead = true
					if len(node.Args) > 0 {
						if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if v, err := strconv.Unquote(lit.Value); err == nil && (v == ".." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../")) {
								add("external-fixture", fmt.Sprintf("test reads parent-directory path %q via os.%s", v, fn))
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

	if isTest && hasFileIORead && hasParentRef {
		add("external-fixture", fmt.Sprintf("test performs filesystem reads combined with parent-directory traversal (%q)", parentRefSample))
	}

	return findings, nil
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
