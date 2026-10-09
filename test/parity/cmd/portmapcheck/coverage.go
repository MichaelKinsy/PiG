package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// digestOf hashes the cited Go files and every Go file of the packages that hold the evidence tests.
// digestVersion changes whenever the way evidence tests run changes (environment, flags), so a cached result from the old way is not reused.
const digestVersion = "evidence-env-2"

func digestOf(root string, res result) string {
	h := sha256.New()
	h.Write([]byte(digestVersion + "\x00"))
	files := append([]string{}, res.Cited...)
	for _, t := range res.Tests {
		dir, _, _ := strings.Cut(t, "#")
		matches, _ := filepath.Glob(filepath.Join(root, dir, "*.go")) // the evidence package: its tests and the sources they link
		for _, m := range matches {
			rel, _ := filepath.Rel(root, m)
			files = append(files, filepath.ToSlash(rel))
		}
	}
	files = append(files, oracleInstall)
	sort.Strings(files)
	prev := ""
	for _, f := range files {
		if f == prev {
			continue
		}
		prev = f
		b, _ := os.ReadFile(filepath.Join(root, f))
		h.Write([]byte(f + "\x00" + strconv.Itoa(len(b)) + "\x00"))
		h.Write(b)
	}
	for _, t := range res.Tests {
		h.Write([]byte(t + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// coverageProblem returns why a proven row has no fresh, positive coverage record, or "".
func coverageProblem(res result, e coverageEntry) string {
	if len(res.Tests) == 0 {
		return "COVERAGE: no evidence test names the Go files of this row"
	}
	if e.Failed != "" {
		return "COVERAGE: the evidence tests of " + e.Failed + " fail"
	}
	for _, f := range coverageFiles(res) {
		if e.Covered[f] <= 0 {
			return "COVERAGE: the evidence tests execute no statement of " + f
		}
	}
	return ""
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

// coverageFor returns the coverage record of a proven row: the cached one for the current digest, or the result of running its evidence
// tests with -coverpkg over the cited packages (cached under build/portmap-coverage, keyed by the digest of the cited files and tests).
func coverageFor(root string, r result) coverageEntry {
	digest := digestOf(root, r)
	cacheFile := filepath.Join(root, "build", "portmap-coverage", digest+".json")
	if b, err := os.ReadFile(cacheFile); err == nil {
		var e coverageEntry
		if json.Unmarshal(b, &e) == nil && e.Digest == digest {
			return e
		}
	}
	e := runRow(root, r, digest)
	if b, err := json.Marshal(e); err == nil && os.MkdirAll(filepath.Dir(cacheFile), 0o755) == nil {
		_ = os.WriteFile(cacheFile, b, 0o644)
	}
	return e
}

// runRow executes the evidence tests of one row and records the statements they execute in each cited file.
func runRow(root string, r result, digest string) coverageEntry {
	covered := map[string]int{}
	failed := ""
	byDir := map[string][]string{}
	for _, t := range r.Tests {
		dir, name, _ := strings.Cut(t, "#")
		byDir[dir] = append(byDir[dir], name)
	}
	pkgs := map[string]bool{}
	for _, f := range coverageFiles(r) {
		pkgs["./"+path.Dir(f)] = true
	}
	var pkgList []string
	for p := range pkgs {
		pkgList = append(pkgList, p)
	}
	sort.Strings(pkgList)
	for dir, names := range byDir {
		sort.Strings(names)
		profile := filepath.Join(os.TempDir(), "portmap-"+strconv.Itoa(os.Getpid())+".cover")
		cmd := exec.Command("go", "test", "-count=1", "-run", "^("+strings.Join(names, "|")+")$", "-coverpkg="+strings.Join(pkgList, ","), "-coverprofile="+profile, "./"+dir)
		cmd.Dir = root
		scratch, err := os.MkdirTemp("", "portmap-agent-")
		if err != nil {
			failed = dir
			continue
		}
		cmd.Env = evidenceEnv(scratch)
		_, err = cmd.CombinedOutput()
		_ = os.RemoveAll(scratch)
		if err != nil {
			failed = dir
			continue
		}
		prof, err := os.ReadFile(profile)
		if err != nil {
			failed = dir
			continue
		}
		_ = os.Remove(profile)
		for line := range strings.SplitSeq(string(prof), "\n") {
			file, rest, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			fields := strings.Fields(rest)
			if len(fields) != 3 {
				continue
			}
			stmts, _ := strconv.Atoi(fields[1])
			count, _ := strconv.Atoi(fields[2])
			if count > 0 {
				covered[repoRel(root, file)] += stmts
			}
		}
	}
	return coverageEntry{Digest: digest, Covered: covered, Failed: failed}
}

// repoRel strips the module path from a coverage profile file name.
func repoRel(root, file string) string {
	b, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	for line := range strings.SplitSeq(string(b), "\n") {
		if mod, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimPrefix(file, strings.TrimSpace(mod)+"/")
		}
	}
	return file
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
