package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// coverageEntry records, for one Pi file, the content digest of its cited Go files and evidence tests, and the statements the evidence
// tests executed in each mapped cited file (rule 3: coverage of that file from the named tests is above zero).
type coverageEntry struct {
	Digest  string         `json:"digest"`
	Covered map[string]int `json:"covered"`
	// Failed names an evidence test package whose named tests fail: the row is not proven while they are red.
	Failed string `json:"failed,omitempty"`
	// Killed records that a reviewed mutant of a marker row's cited file made its tests fail; the digest covers the mutants.
	Killed bool `json:"killed,omitempty"`
}

// evidenceTests lists the Go tests (`dir#TestName`) that carry a proven row: the evidence of its ledger members, or the `// pi:` tests of
// a file with no member.
func evidenceTests(_ string, pi string, inv *inventory, markers map[string][]string) []string {
	set := map[string]bool{}
	for _, id := range inv.membersOf(pi) {
		for _, e := range inv.mapping[id].Evidence {
			if rest, ok := strings.CutPrefix(e, "test:"); ok {
				file, name, _ := strings.Cut(rest, "#")
				if name != "" {
					set[path.Dir(file)+"#"+name] = true
				}
			}
		}
	}
	for _, t := range markers[pi] {
		set[t] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// oracleInstall is the npm lockfile of the installed Pi oracle packages that evidence tests run under Node. Its bytes join every digest, so
// installing, removing or upgrading the oracle re-runs the tests instead of reusing a record made without it.
const oracleInstall = "extensions/sdk-ts/node_modules/.package-lock.json"

// digestSalt changes whenever the way evidence tests run changes (environment, flags), so a cached result from the old way is not reused.
const digestSalt = "evidence-env-3"

// digester hashes the files a row's record depends on, reading each file once per check however many rows name it.
type digester struct {
	root string
	sums map[string]string
	pkgs map[string][]string
}

func newDigester(root string) *digester {
	return &digester{root: root, sums: map[string]string{}, pkgs: map[string][]string{}}
}

// sum is the content digest of one repository file; a missing file digests as empty content.
func (d *digester) sum(rel string) string {
	if s, ok := d.sums[rel]; ok {
		return s
	}
	b, _ := os.ReadFile(filepath.Join(d.root, rel))
	h := sha256.Sum256(b)
	s := strconv.Itoa(len(b)) + ":" + hex.EncodeToString(h[:])
	d.sums[rel] = s
	return s
}

// goFiles lists the Go files of an evidence package: its tests and the sources they link.
func (d *digester) goFiles(dir string) []string {
	if f, ok := d.pkgs[dir]; ok {
		return f
	}
	matches, _ := filepath.Glob(filepath.Join(d.root, dir, "*.go"))
	files := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, _ := filepath.Rel(d.root, m)
		files = append(files, filepath.ToSlash(rel))
	}
	d.pkgs[dir] = files
	return files
}

// of hashes the cited Go files, every Go file of the packages that hold the evidence tests, the tests, and for a marker row the
// mutants it must kill (a killed mutant is part of the record).
func (d *digester) of(res result, mutants []mutant) string {
	h := sha256.New()
	h.Write([]byte(digestSalt + "\x00"))
	files := append([]string{}, res.Cited...)
	for _, t := range res.Tests {
		dir, _, _ := strings.Cut(t, "#")
		files = append(files, d.goFiles(dir)...)
	}
	files = append(files, oracleInstall)
	sort.Strings(files)
	files = slices.Compact(files)
	for _, f := range files {
		h.Write([]byte(f + "\x00" + d.sum(f) + "\x00"))
	}
	for _, t := range res.Tests {
		h.Write([]byte(t + "\x00"))
	}
	if res.Marker {
		b, _ := json.Marshal(mutants)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// coverageProblem returns why a proven row has no fresh, positive coverage record, or "". A cited file that declares no function body
// holds no statement a test can execute (Go coverage instruments only function bodies), so rule 3 asks nothing of it beyond the green
// evidence run: its constants, types and embedded data are proven by the evidence tests of the members that map to them.
func coverageProblem(root string, res result, e coverageEntry) string {
	if len(res.Tests) == 0 {
		return "COVERAGE: no evidence test names the Go files of this row"
	}
	if e.Failed != "" {
		return "COVERAGE: the evidence tests of " + e.Failed + " fail"
	}
	for _, f := range coverageFiles(res) {
		if e.Covered[f] <= 0 && hasStatements(filepath.Join(root, f)) {
			return "COVERAGE: the evidence tests execute no statement of " + f
		}
	}
	return ""
}

// hasStatements reports whether a Go file declares a function body (a function, a method or a function literal), the only code
// coverage counts. An unreadable file counts as having statements, so it is never exempt.
func hasStatements(path string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return true
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			found = found || n.Body != nil
		case *ast.FuncLit:
			found = true
		}
		return !found
	})
	return found
}

// coverageFiles are the cited non-test Go files the evidence tests must execute: the mapped ones, or every cited one when no member maps.
func coverageFiles(res result) []string {
	if len(res.Hit) > 0 {
		return res.Hit
	}
	var out []string
	for _, f := range res.Cited {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

// modulePath is the module path of the repository under root, the prefix of every file name in a coverage profile.
func modulePath(root string) string {
	b, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	for line := range strings.SplitSeq(string(b), "\n") {
		if mod, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(mod)
		}
	}
	return ""
}

// agentDirVariables name the directories a Pig or Pi process reads credentials and settings from; HOME is their fallback.
var agentDirVariables = []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME", "HOME"}

// toolchainHomes name the per-user directories language toolchains resolve from HOME (Cargo, Rustup, mise, npm, uv, pip, XDG), with
// their default location below HOME. Pointing HOME at a scratch tree would hide the installed toolchains from the extension conformance
// tests, which build Rust and Python SDK fixtures, so each keeps its real location. None holds Pig or Pi credentials: those live in the
// agent directories, which stay scratch.
var toolchainHomes = []struct{ name, rel string }{
	{"CARGO_HOME", ".cargo"}, {"RUSTUP_HOME", ".rustup"}, {"npm_config_cache", ".npm"}, {"XDG_CONFIG_HOME", ".config"},
	{"XDG_DATA_HOME", ".local/share"}, {"XDG_CACHE_HOME", ".cache"}, {"XDG_STATE_HOME", ".local/state"}, {"GOPATH", "go"},
}

// evidenceEnv is the caller's environment with every agent-directory variable pointed into scratch, so an evidence test that writes
// credentials or settings cannot reach the caller's real agent directory (a package without a scoping TestMain inherits them). The Go
// build and module caches and the toolchain homes keep their resolved locations, which would otherwise move with HOME.
func evidenceEnv(scratch string) []string {
	env := make([]string, 0, len(os.Environ())+len(agentDirVariables)+2)
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		if os.Getenv(name) == "" {
			if out, err := exec.Command("go", "env", name).Output(); err == nil {
				env = append(env, name+"="+strings.TrimSpace(string(out)))
			}
		}
	}
	home, _ := os.UserHomeDir()
	for _, th := range toolchainHomes {
		if os.Getenv(th.name) == "" && home != "" {
			env = append(env, th.name+"="+filepath.Join(home, filepath.FromSlash(th.rel)))
		}
	}
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		emptyToolchain := value == "" && slices.ContainsFunc(toolchainHomes, func(th struct{ name, rel string }) bool { return th.name == name })
		if !slices.Contains(agentDirVariables, name) && !emptyToolchain {
			env = append(env, kv)
		}
	}
	for _, name := range agentDirVariables {
		env = append(env, name+"="+filepath.Join(scratch, strings.ToLower(name)))
	}
	return env
}

// lastLines is the tail of a failed evidence run, so the check names why it failed instead of only that it did.
func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
