package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRoot writes a /proc and /sys tree as a pod on a shared node sees it: every core allowed, a CFS quota.
func fakeRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	base := map[string]string{
		"proc/self/status":                 "Name:\tk8sbench\nCpus_allowed:\tff\nCpus_allowed_list:\t0-7\n",
		"proc/cpuinfo":                     "processor\t: 0\nmodel name\t: Example CPU @ 2.00GHz\n\nprocessor\t: 1\nmodel name\t: Example CPU @ 2.00GHz\n",
		"proc/stat":                        "cpu  1 2 3 4 5 6 7 8 9 10\ncpu0 0 0 0 100 0 0 0 0 0 0\ncpu1 0 0 0 100 0 0 0 0 0 0\n",
		"proc/sys/kernel/osrelease":        "6.1.0-test\n",
		"proc/loadavg":                     "0.50 0.40 0.30 1/100 42\n",
		"sys/devices/system/cpu/online":    "0-7\n",
		"sys/fs/cgroup/cgroup.controllers": "cpu memory\n",
		"sys/fs/cgroup/cpu.stat":           "usage_usec 1000\nuser_usec 800\nsystem_usec 200\nnr_periods 10\nnr_throttled 2\nthrottled_usec 3000\n",
		"sys/fs/cgroup/cpu.max":            "200000 100000\n",
		"sys/fs/cgroup/memory.max":         "8589934592\n",
		"sys/devices/system/cpu/cpu2/cpufreq/scaling_governor": "performance\n",
	}
	maps.Copy(base, files)
	for path, content := range base {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// shellProfile is a profile whose commands are shell one-liners: seeds write the meta file durable-bench's seed
// writes, samples print a result line among other output, and every command appends its name to calls.log.
func shellProfile(dir string) *profile {
	calls := filepath.Join(dir, "calls.log")
	seed := func(name string) []string {
		return []string{"sh", "-c", fmt.Sprintf(`echo "seed %s {turns}" >> %s; echo progress >&2; printf '{"target":"%s-bench","turns":{turns},"fingerprint":"fp{turns}","seedMs":1500,"bytes":10}' > {dir}/meta.json`, name, calls, name)}
	}
	sample := func(name string, open int) []string {
		return []string{"sh", "-c", fmt.Sprintf(`echo "sample %s {turns} {sample}" >> %s; echo "not a result"; echo '{"target":"%s-bench","version":"v9","turns":{turns},"sample":{sample},"open":%d,"turn":[10,20,30],"fingerprint":"after"}'`, name, calls, name, open)}
	}
	return &profile{Name: "shell", Rule: "test", Fingerprints: map[string]string{"5": "fp5"}, Targets: []target{
		{Name: "pig", Role: rolePig, Seed: seed("pig"), SeedMeta: "{dir}/meta.json", Sample: sample("pig", 1)},
		{Name: "pi", Role: roleReference, Seed: seed("pi"), SeedMeta: "{dir}/meta.json", Sample: sample("pi", 2)},
	}}
}

func testEnv(t *testing.T, root string) podEnv {
	t.Helper()
	install := t.TempDir()
	if err := os.WriteFile(filepath.Join(install, "COMMIT"), []byte("0123456789abcdef0123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "VERSIONS"), []byte("go=go1.27.1\nnode=v24.19.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "ARTIFACTS.sha256"), []byte(strings.Repeat("a", 64)+"  bin/durableperf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return podEnv{sys: system{root: root}, root: install, data: t.TempDir(), node: "node-a", pod: "pod-a", pinWait: func(context.Context) error { return nil },
		now: func() time.Time { return time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC) }, getenv: func(string) string { return "" },
		nodeRuntime: func(context.Context) string { return "v24.19.0" }}
}

func TestPodSessionInterleavesAndRetags(t *testing.T) {
	root := fakeRoot(t, nil)
	env := testEnv(t, root)
	env.results = t.TempDir()
	scratch := t.TempDir()
	pl := &plan{Run: "k8sbench-cmp-test", Mode: modeCompare, Profile: shellProfile(scratch), Targets: []string{"pig", "pi"}, Turns: []int{5, 9},
		Rounds: 3, CPUs: 2, Pin: "none", Commit: "0123456789", StepTimeoutSeconds: 30}
	var out bytes.Buffer
	if err := runPod(context.Background(), pl, env, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	calls, _ := os.ReadFile(filepath.Join(scratch, "calls.log"))
	// Seeds rotate per size; rounds are outermost, sizes inside, and each round rotates the target order.
	want := strings.Join([]string{
		"seed pig 5", "seed pi 5", "seed pi 9", "seed pig 9",
		"sample pig 5 0", "sample pi 5 0", "sample pig 9 0", "sample pi 9 0",
		"sample pi 5 1", "sample pig 5 1", "sample pi 9 1", "sample pig 9 1",
		"sample pig 5 2", "sample pi 5 2", "sample pig 9 2", "sample pi 9 2",
	}, "\n") + "\n"
	if string(calls) != want {
		t.Errorf("order:\n%s\nwant:\n%s", calls, want)
	}
	full := slices.Clone(out.Bytes())
	log, err := parsePodLog(&out)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.complete(); err != nil {
		t.Fatal(err)
	}
	if len(log.samples) != 12 || len(log.seeds) != 4 || len(log.cpu) != 16 {
		t.Fatalf("samples %d seeds %d cpu %d", len(log.samples), len(log.seeds), len(log.cpu))
	}
	first := log.samples[0].raw
	if want := []string{"t", "target", "bench_target", "version", "turns", "sample", "open", "turn", "fingerprint", "gomaxprocs", "cpuModel", "cpuFeatures"}; !slices.Equal(first.keys, want) {
		t.Errorf("sample line fields %v, want %v", first.keys, want)
	}
	if first.str("target") != "pig" || first.str("bench_target") != "pig-bench" || first.str("version") != "0123456789abcdef0123" {
		t.Errorf("pig sample retag: %s", first.values)
	}
	if log.samples[1].raw.str("version") != "v9" {
		t.Error("the reference keeps its own version")
	}
	seed := log.seeds[0]
	if seed.str("from") != "empty" || seed.str("fingerprint") != "fp5" || seed.str("target") != "pig" {
		t.Errorf("seed line %s", seed.values)
	}
	f := log.facts
	if f.str("cpuSet") != "shared" || f.str("cpusAllowed") != "0-7" || f.str("cpuMax") != "200000 100000" || f.str("kernel") != "6.1.0-test" || f.str("cpuModel") != "Example CPU @ 2.00GHz" || f.str("node") != "node-a" {
		t.Errorf("facts %v", f.keys)
	}
	var versions, artifacts map[string]string
	f.get("versions", &versions)
	f.get("artifacts", &artifacts)
	if versions["node"] != "v24.19.0" || artifacts["bin/durableperf"] != strings.Repeat("a", 64) {
		t.Errorf("versions %v artifacts %v", versions, artifacts)
	}
	var throttled int64 = -1
	log.cpu[0].get("nrThrottled", &throttled)
	if throttled != 0 {
		t.Errorf("a step's throttling is the cpu.stat delta around it, got %d", throttled)
	}
	copied, err := os.ReadFile(filepath.Join(env.results, pl.Run, "pod.jsonl"))
	if err != nil || !bytes.Equal(copied, full) {
		t.Errorf("the results volume holds a copy of every line: %v", err)
	}
}

func TestPodSessionReportsAFailingStep(t *testing.T) {
	env := testEnv(t, fakeRoot(t, nil))
	prof := shellProfile(t.TempDir())
	prof.Targets[1].Sample = []string{"sh", "-c", "echo boom-from-stderr >&2; exit 3"}
	pl := &plan{Run: "r", Mode: modeCompare, Profile: prof, Targets: []string{"pig", "pi"}, Turns: []int{5}, Rounds: 1, CPUs: 2, Pin: "none", StepTimeoutSeconds: 30}
	var out bytes.Buffer
	err := runPod(context.Background(), pl, env, &out)
	if err == nil || !strings.Contains(err.Error(), "boom-from-stderr") || !strings.Contains(err.Error(), "sample pi at 5 turns") {
		t.Fatalf("want the failing step and its stderr, got %v", err)
	}
	prof.Targets[1].Sample = []string{"sh", "-c", "echo no result here"}
	err = runPod(context.Background(), pl, env, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no result line") {
		t.Fatalf("a sample without a result line must fail, got %v", err)
	}
}

func TestPodSessionRefusesAnotherCommit(t *testing.T) {
	env := testEnv(t, fakeRoot(t, nil))
	pl := &plan{Run: "r", Mode: modeCompare, Profile: shellProfile(t.TempDir()), Targets: []string{"pig", "pi"}, Turns: []int{5}, Rounds: 1, CPUs: 2, Pin: "none", Commit: "fedcba9876", StepTimeoutSeconds: 30}
	if err := runPod(context.Background(), pl, env, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not the requested commit") {
		t.Fatalf("got %v", err)
	}
	for _, c := range []struct {
		image, want string
		ok          bool
	}{{"0123456789ab", "0123456", true}, {"0123456789ab-dirty", "0123456", false}, {"0123456789ab-dirty", "0123456789-dirty", true}, {"0123456789ab", "0123", false}} {
		if sameCommit(c.image, c.want) != c.ok {
			t.Errorf("sameCommit(%q, %q) != %v", c.image, c.want, c.ok)
		}
	}
}

// On a shared node the session pins to the quietest allowed cores; with exclusive cores it uses them as they are.
func TestPodPinning(t *testing.T) {
	if _, err := lookTaskset(); err != nil {
		t.Skip("taskset is not installed")
	}
	// gomaxprocs is the number of measured cores, at most the pod's 2 CPUs.
	cases := []struct {
		status, pin, method, cpus string
		gomaxprocs                int
	}{
		{"Cpus_allowed_list:\t0-7\n", "auto", "quietest", "2,5", 2},
		{"Cpus_allowed_list:\t6-7\n", "auto", "all-allowed", "6,7", 2},
		{"Cpus_allowed_list:\t0-7\n", "3,4", "list", "3,4", 2},
		{"Cpus_allowed_list:\t0-7\n", "3", "list", "3", 1},
		{"Cpus_allowed_list:\t0-7\n", "1-4", "list", "1-4", 2},
		{"Cpus_allowed_list:\t6\n", "auto", "all-allowed", "6", 1},
	}
	before, after := "cpu  0 0 0 0 0 0 0 0 0 0\n", "cpu  0 0 0 0 0 0 0 0 0 0\n"
	for i := range 8 {
		// Between the two readings cores 2 and 5 stay idle and the others are half busy.
		idle := 50
		if i == 2 || i == 5 {
			idle = 100
		}
		before += fmt.Sprintf("cpu%d 0 0 0 0 0 0 0 0 0 0\n", i)
		after += fmt.Sprintf("cpu%d 0 0 %d %d 0 0 0 0 0 0\n", i, 100-idle, idle)
	}
	for _, c := range cases {
		root := fakeRoot(t, map[string]string{"proc/self/status": c.status, "proc/stat": before})
		env := testEnv(t, root)
		env.pinWait = func(context.Context) error {
			return os.WriteFile(filepath.Join(root, "proc/stat"), []byte(after), 0o644)
		}
		var out bytes.Buffer
		s := &session{plan: &plan{CPUs: 2, Pin: c.pin}, env: env, out: &out}
		if err := s.choosePin(context.Background()); err != nil {
			t.Fatal(err)
		}
		var line object
		if err := json.Unmarshal(out.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.str("method") != c.method || line.str("cpus") != c.cpus {
			t.Errorf("%q pin=%s: got %s %s, want %s %s", c.status, c.pin, line.str("method"), line.str("cpus"), c.method, c.cpus)
		}
		var gmp int
		if !line.get("gomaxprocs", &gmp) || gmp != c.gomaxprocs || s.gomaxprocs != c.gomaxprocs {
			t.Errorf("%q pin=%s: gomaxprocs %d (session %d), want %d", c.status, c.pin, gmp, s.gomaxprocs, c.gomaxprocs)
		}
		if c.method == "quietest" {
			var g map[string]string
			line.get("governors", &g)
			if g["2"] != "performance" {
				t.Errorf("governor of a pinned core: %v", g)
			}
		}
	}
	root := fakeRoot(t, nil)
	s := &session{plan: &plan{CPUs: 2, Pin: "9"}, env: testEnv(t, root), out: &bytes.Buffer{}}
	if err := s.choosePin(context.Background()); err == nil {
		t.Error("a pin list outside the allowed CPUs must be refused")
	}
}

// A cpu line carries usageUsec only when the cgroup's usage was read before and after the step.
func TestPodCPULineOmitsAnUnreadUsage(t *testing.T) {
	root := fakeRoot(t, map[string]string{"sys/fs/cgroup/cpu,cpuacct/cpu.stat": "nr_periods 7\nnr_throttled 0\nthrottled_time 0\n"})
	if err := removeFile(root, "sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Fatal(err)
	}
	env := testEnv(t, root)
	pl := &plan{Run: "r", Mode: modeCompare, Profile: shellProfile(t.TempDir()), Targets: []string{"pig", "pi"}, Turns: []int{5}, Rounds: 1, CPUs: 2, Pin: "none", StepTimeoutSeconds: 30}
	var out bytes.Buffer
	if err := runPod(context.Background(), pl, env, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	log, err := parsePodLog(&out)
	if err != nil {
		t.Fatal(err)
	}
	if len(log.cpu) == 0 || len(log.samples) == 0 {
		t.Fatalf("cpu %d samples %d", len(log.cpu), len(log.samples))
	}
	for _, c := range log.cpu {
		if c.has("usageUsec") || !c.has("nrPeriods") {
			t.Errorf("cpu line %v", c.keys)
		}
	}
	for _, s := range log.samples {
		if s.usageMs != nil {
			t.Errorf("sample %s/%d has a cgroup usage of %v ms that was never read", s.target, s.index, *s.usageMs)
		}
	}
}

func TestPodRefusesAnotherNodeRelease(t *testing.T) {
	for _, c := range []struct {
		runtime string
		refused string
	}{
		// Node 26.11.0 renames node:sqlite's DatabaseSync and StatementSync: the artifacts pin v24.19.0.
		{"v26.11.0", "pin Node v24.19.0 but the pod runs Node \"v26.11.0\""},
		// No node on PATH refuses before anything runs, not at the first Node step.
		{"", "pin Node v24.19.0 but the pod runs Node \"\""},
		// The bundle's own VERSIONS names the release in the form node --version prints.
		{"v" + nodeVersion, ""},
	} {
		env := testEnv(t, fakeRoot(t, nil))
		if err := os.WriteFile(filepath.Join(env.root, "VERSIONS"), []byte(bundleVersions("go1.27.1", "0.42.0", "1.1.0", "", "amd64")), 0o644); err != nil {
			t.Fatal(err)
		}
		env.nodeRuntime = func(context.Context) string { return c.runtime }
		scratch := t.TempDir()
		pl := &plan{Run: "k8sbench-cmp-test", Mode: modeCompare, Profile: shellProfile(scratch), Targets: []string{"pig", "pi"}, Turns: []int{5},
			Rounds: 1, CPUs: 2, Pin: "none", Commit: "0123456789", StepTimeoutSeconds: 30}
		err := runPod(context.Background(), pl, env, &bytes.Buffer{})
		calls, _ := os.ReadFile(filepath.Join(scratch, "calls.log"))
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%q: the pinned release is refused: %v", c.runtime, err)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("%q: err %v", c.runtime, err)
		case c.refused != "" && len(calls) != 0:
			t.Errorf("%q: a refused session ran %s", c.runtime, calls)
		}
	}
}

// Every step runs with GOMAXPROCS set to the number of measured cores, at most the pod's CPUs, whatever the pod's
// environment says, and every seed and sample line names it with the node type, because a Go program's CPU time follows
// both. Without a pin the measured cores are the 8 allowed ones: a 2-CPU pod still runs 2 Ps, not one per core of the
// node.
func TestStepsRunWithAnExplicitGOMAXPROCS(t *testing.T) {
	t.Setenv("GOMAXPROCS", "3")
	for _, c := range []struct{ cpus, want int }{{2, 2}, {16, 8}} {
		root := fakeRoot(t, map[string]string{"proc/cpuinfo": "processor\t: 0\nmodel name\t: Example CPU @ 2.00GHz\nflags\t\t: fpu sse2 avx avx2 bmi2 avx512f avx512bw gfni\n"})
		env := testEnv(t, root)
		scratch := t.TempDir()
		prof := shellProfile(scratch)
		for i := range prof.Targets {
			prof.Targets[i].Sample = []string{"sh", "-c", `echo '{"turns":5,"open":1,"turn":[10,20],"fingerprint":"after","env":"'"$GOMAXPROCS"'"}'`}
		}
		pl := &plan{Run: "k8sbench-cmp-test", Mode: modeCompare, Profile: prof, Targets: []string{"pig", "pi"}, Turns: []int{5},
			Rounds: 1, CPUs: c.cpus, Pin: "none", Commit: "0123456789", StepTimeoutSeconds: 30}
		var out bytes.Buffer
		if err := runPod(context.Background(), pl, env, &out); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		log, err := parsePodLog(&out)
		if err != nil {
			t.Fatal(err)
		}
		want := strconv.Itoa(c.want)
		var gmp int
		if !log.pin.get("gomaxprocs", &gmp) || gmp != c.want {
			t.Errorf("cpus %d: pin line gomaxprocs %d, want %d", c.cpus, gmp, c.want)
		}
		if got := log.facts.str("cpuFeatures"); got != "avx avx2 avx512f avx512bw gfni bmi2" {
			t.Errorf("facts cpuFeatures %q", got)
		}
		for _, s := range log.samples {
			var n int
			if s.raw.str("env") != want || !s.raw.get("gomaxprocs", &n) || n != c.want || s.raw.str("cpuModel") != "Example CPU @ 2.00GHz" || s.raw.str("cpuFeatures") != "avx avx2 avx512f avx512bw gfni bmi2" {
				t.Errorf("cpus %d: sample %s: the process saw GOMAXPROCS %q, want %s; line %v", c.cpus, s.target, s.raw.str("env"), want, s.raw.values)
			}
		}
		for _, seed := range log.seeds {
			var n int
			if !seed.get("gomaxprocs", &n) || n != c.want || seed.str("cpuModel") == "" {
				t.Errorf("cpus %d: seed line %v", c.cpus, seed.values)
			}
		}
	}
}
