// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/profiling"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Startup allocation budgets, from PIG_PROFILE=metrics (runtime/metrics /gc/heap/allocs:*) of the built binary. They only
// ratchet down: a change that lowers the measurement lowers its budget in the same commit.
//
// versionAlloc* is `pig --version`: runtime start, package initializers and argument parsing, no session.
// firstPromptAlloc* is a warm print-mode run of one prompt against the faux provider, from process start to exit: the
// model registry, resources, the system prompt, the session file and one provider round trip. The first run of a fresh
// agent directory also stages the embedded docs and SDKs, so the measurement starts from a second run and takes the
// smallest of three.
//
// The measured process runs with GOMAXPROCS=1. With several Ps the runtime/metrics totals at exit move with goroutine scheduling, while a heap profile of the same runs holds the same allocations: on a 24-thread host a warm first prompt measured 5.63-5.95 MB and 34,369-37,097 objects in single runs, about 6% apart. With one P the totals repeat to within 0.5%: pig --version measured 790,752 bytes in 4,982 objects before the metric was corrected, and the first prompt 2,476,912 bytes in 18,418 objects.
//
// Re-measured on 2026-10-13 (owner decision) after the metric was corrected: CollectMetrics now flushes per-P allocation caches with runtime.ReadMemStats (commit 8b9e467f20), so allocations the old figure missed are counted. Building e876a97f8a with and without only that change moved the warm first prompt from 2,444,736 bytes / 18,104 objects to 3,039,144-3,073,904 bytes / 20,434-20,621 objects, and --version from 790,752 / 4,982 to 1,273,520 / 7,128. The budgets are the in-test measurement at the tip (--version 1,010,880 / 6,572; warm first prompt 3,185,400 / 22,222) plus under 2% headroom. The AWS SDK fork (third_party/awssdk/FORK.md) compiles the endpoint partition expressions on first use, which lowered them to --version 746,056 / 4,152 and warm first prompt 2,913,624 / 19,816 (max of three test runs); the budgets are those plus under 2%.
//
// Budgets are per GOOS because the allocation totals differ by platform (different runtime, syscall and os code paths): Linux amd64 allocates about 34 KB more for --version and about 190 KB / 1,800 objects less for a warm first prompt than macOS arm64. Measured with the test's own in-test numbers, max of 3 runs, on the Linux lab host (Linux amd64, Go 1.22 toolchain driving the pinned one) and on the Mac, at 375de4a98c (d048ed0989^, before the AWS fork) and 7d9a62e919 (integrate-042 tip): --version bytes Linux-Mac was +33.9 KB at 375de4a98c and +34.1 KB at the tip, objects -3 and -1; first prompt bytes -194 KB and -191 KB, objects -1,812 and -1,836. The gap is constant across both commits, so it is a platform difference, not a regression. darwin keeps its values; linux (and every other GOOS) uses the Linux tip maximum (--version 766,152 / 4,055; first prompt 2,673,072 / 17,795) plus under 2%.
//
// Linking one YAML library (go.yaml.in/yaml/v3 only, not also gopkg.in/yaml.v3) drops the second copy's package initializers. On the Linux lab host at the wave-16 merge 4d737d59cc, --version moved from 751,152 bytes / 4,065 objects to 730,872 / 3,830 with TMPDIR=/tmp, and from 758,488 / 4,088 to 727,728 / 3,843 with a TMPDIR two levels deeper (the wave gate's); the linux --version budget is the larger of each plus under 2%. With the skill walk no longer resolving missing roots, the warm first prompt measured 2,624,384 / 17,366 and 2,670,872 / 17,757 in the same two places; its budget stays, as the gate's measurement plus 2% rounds up to it.
//
// From 2026-10-13 the test measures at a declared place (startupFixture: the working directory three components below /, whatever
// TMPDIR is), and a deeper working directory costs about 13 objects per level instead of 205 (TestStartupAllocationPerDirectoryLevel).
// On the Linux lab host at the int-120 line, max of 3 runs: --version 727,328 bytes / 3,841 objects, warm first prompt 2,541,264 / 16,508.
// The linux budgets are those plus under 2%. darwin keeps its values until they are measured there.
//
// 0.5.2 batch head 28732512d6 measured --version 727,552 bytes / 3,894 objects on the Linux host (0.5.1: 714,872 / 3,768), 122 more package-init
// allocations (GODEBUG=inittrace=1), and the Windows runner read 5,803 objects against its 5,800. The batch added no init work of its own
// beyond the SDK's pi-tui engine copy and an init that built a TLS config; the rest was the tui keybinding table, a map literal of slice
// literals that the engine copy doubled. The table is now package-level data and the TLS config is static: init allocations 4,031 against
// 4,001 at 0.5.1 (4,123 before), --version 726,896-732,472 bytes / 3,801-3,808 objects in three runs, so the linux object budget is 3,808
// plus under 2%. The Windows budgets keep their values until the runner measures them.
//
// The Windows runner then read 827,800 bytes / 5,744 objects (every run of ten, 827,800-828,280), over its 824,000, on main and on the train alike.
// internal/nodeerrno built libuv's code table and the errno names at every start, 9,480 bytes in 16 allocations
// (GODEBUG=inittrace=1, Windows build under Wine) of a table only an error path reads. They are built on first use: init 616,296 -> 607,112
// bytes, pig --version 756.5-761.9 KB -> 747.8-753.2 KB under Wine, Linux 717,952-723,688 bytes / 3,794-3,802 objects in two runs, so the
// linux byte budget is 723,688 plus under 2%.
//
// Owner-approved raise (2026-10-25) of the linux warm-first-prompt object budgets, 16,820 -> 17,700 and (models.json) 18,830 -> 21,800: PIG_TEST_FAUX now registers the
// harness's test-faux provider through the model registry, as Pi's parity harness registers it from an extension (registerHarnessTestFaux), so the measured run carries one
// more registered provider. Measured on the Linux lab host, warm first prompt: 17,659 objects / 2,490,176 bytes (main 15,197 / 2,570,920), with models.json 21,771 / 2,924,904
// (main 18,688 / 2,914,688). The extra objects are Pi-equivalent work: the deferred registration refresh pass (Models.Refresh about 640 objects, refresh signals about 345,
// provider refresh phase about 165, availability reload about 250) and the cold json type caches for the registered provider's config (about 525). The registered object's catalog
// record is composed once per registration (it was once per read: 1,569 -> 746 allocs for a registration and six reads, BenchmarkRegisterExtensionProviderAndRead). Byte budgets and
// production (no PIG_TEST_FAUX) budgets are unchanged. Profiles: xd670 ~/tern-v5/trees/faux-reg/tmp/prof1 against ~/tern-v5/trees/faux-main/tmp/prof0.
//
// Platform siblings of that raise (the same owner approval, 2026-10-25): darwin models.json 18,950 -> 21,850, windows warm first prompt 18,950 -> 21,100 and windows models.json
// 22,370 -> 25,200, each about 0.5% over the measured value, as the Linux raises are. Measured on the hosted runners, min of 3, main f2f028f845 against the batch 4a384df786 (diag runs 40304098670
// and 40305356345; the 0.6.0 CI run 40287205705 read 21,756 on macOS and 20,980 / 25,098 on Windows): warm first prompt Windows 18,363 -> 21,001, macOS 15,236 -> 17,658 (the darwin budget of 20,200 already
// holds it), Linux 15,204 -> 17,596; with models.json Windows 21,843 -> 25,100, macOS 18,724 -> 21,752, Linux 18,703 -> 21,732. A control run without PIG_TEST_FAUX (a models.json local provider, print mode,
// --no-extensions) is identical on main and the batch within noise (Windows 45,381-45,391 against 45,374-45,390 objects, macOS 42,004-42,022 against 42,007-42,013, Linux 41,912-41,916 against 41,919-41,921) and
// the macOS and Windows allocation profiles of the two differ by 14 objects at most in any function, so the whole delta is the registration. Its parts match the Linux profile: Models.Refresh 644-656, refresh signals 344, provider
// refresh phase 166-175, json type caches 523-527. Windows is about 245 objects above Linux because the availability reload checks every provider's auth once more (env lookups 456 -> 684 objects) and an
// os.LookupEnv costs two allocations on Windows and none on Linux. Byte budgets are unchanged.
//
// The 0.6.0 batch (Pi 6c384aa3, the SDK batch, the Type engine) raised --version on the Linux host from main d9210cd1d0's 718,320 bytes / 3,798 objects to 724,168 / 3,821, and the
// Windows runner over its 824,000 (825,016) and its models.json budget (3,268,928 against 3,265,000). GODEBUG=inittrace=1 put 6.6 KB / 28 allocations of it in package initializers: the Type
// engine's member table and its two prototype name sets (4,264 bytes / 12), the JSON Schema keyword sets of ai/google.go (1.2 KB / 6), the tui word selection joiners (256 / 2), chord's default
// delivery scheduler (136 / 2) and the compiled-in registry (304 / 2). Each is now built on first use or is a switch: --version measures 718,056-718,696 bytes / 3,798-3,802 objects (main
// 718,000-718,480 / 3,796-3,799, same test, six runs each; one run of each of the six read 5.3 KB more, the extra OS thread's allocation); with models.json 2,920,472-2,921,736 / 21,720-21,724 (main 2,921,920).
type startupAllocBudget struct{ versionBytes, versionObjects, promptBytes, promptObjects uint64 }

func startupAllocBudgetFor(goos string) startupAllocBudget {
	if goos == "windows" {
		return startupAllocBudget{versionBytes: windowsVersionBytes, versionObjects: windowsVersionObjects, promptBytes: windowsPromptBytes, promptObjects: windowsPromptObjects}
	}
	if goos == "darwin" {
		return startupAllocBudget{versionBytes: 760_000, versionObjects: 4_240, promptBytes: 2_970_000, promptObjects: 20_200}
	}
	return startupAllocBudget{versionBytes: 738_000, versionObjects: 3_880, promptBytes: 2_589_000, promptObjects: 17_700}
}

func TestStartupAllocationBudget(t *testing.T) {
	budget := startupAllocBudgetFor(runtime.GOOS)
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	fixture := newStartupFixture(t)
	ctx := testbudget.Context(t)

	version := fixture.version(ctx, t, fixture.cwd, budget)
	if version.AllocBytes > budget.versionBytes || version.AllocObjects > budget.versionObjects {
		startupBudgetError(t, "pig --version allocated %d bytes in %d objects, budget %d bytes in %d objects", version.AllocBytes, version.AllocObjects, budget.versionBytes, budget.versionObjects)
	}

	fixture.measure(ctx, t, fixture.cwd, startupPrompt...) // stages the embedded docs and SDKs
	best := fixture.warmPrompt(ctx, t, fixture.cwd)
	t.Logf("cwd %s; pig --version: %d bytes, %d objects; first prompt (warm, min of 3): %d bytes, %d objects, %d GC cycles", fixture.cwd, version.AllocBytes, version.AllocObjects, best.AllocBytes, best.AllocObjects, best.GCCycles)
	if best.AllocBytes > budget.promptBytes || best.AllocObjects > budget.promptObjects {
		startupBudgetError(t, "a warm first prompt allocated %d bytes in %d objects, budget %d bytes in %d objects", best.AllocBytes, best.AllocObjects, budget.promptBytes, budget.promptObjects)
	}
}

// startupModelsJSON is a representative models.json: a custom OpenAI-compatible provider with provider and model compat, and an
// override of the model the prompt runs. Startup reads it at least twice (the registry and the first model refresh) and
// validates it against the models.json schema each time, a path the fixture above, which has no models.json, does not reach.
const startupModelsJSON = `{
  "providers": {
    "local": {
      "baseUrl": "http://127.0.0.1:9/v1",
      "api": "openai-completions",
      "apiKey": "local-key",
      "compat": {"supportsDeveloperRole": false, "maxTokensField": "max_tokens"},
      "models": [
        {"id": "local-model", "name": "Local Model", "reasoning": true, "input": ["text"], "contextWindow": 128000, "maxTokens": 8192,
         "cost": {"input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 0},
         "compat": {"supportsUsageInStreaming": false, "thinkingFormat": "qwen"}}
      ]
    },
    "test-faux": {
      "modelOverrides": {"faux-1": {"name": "Faux One", "contextWindow": 64000, "compat": {"supportsStrictMode": false}}}
    }
  }
}
`

// startupModelsAllocBudgetFor bounds a warm first prompt whose agent directory holds startupModelsJSON. Measured with the test's own
// numbers, max of 3 runs, at the commit that builds the models.json schema once per process: on the Linux amd64 lab host 2,942,992 bytes
// / 18,460 objects, on the Mac (darwin arm64) 2,925,824 / 18,578. Before that commit every read built the schema tree again
// (704f0daaac), and the same runs measured 3,253,416 / 23,021 and 3,246,840 / 23,145. With the session file's write buffer sized to
// its records (fa1306137e) they measure 2,883,152 / 18,455 and 2,877,344 / 18,588; the budgets are those plus under 2%.
func startupModelsAllocBudgetFor(goos string) (bytes, objects uint64) {
	if goos == "windows" {
		return windowsModelsPromptBytes, windowsModelsPromptObjects
	}
	if goos == "darwin" {
		return 2_934_000, 21_850
	}
	return 2_940_000, 21_800
}

func TestStartupAllocationBudgetWithModelsJSON(t *testing.T) {
	budgetBytes, budgetObjects := startupModelsAllocBudgetFor(runtime.GOOS)
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	fixture := newStartupFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.agentDir, "models.json"), []byte(startupModelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.Context(t)
	fixture.measure(ctx, t, fixture.cwd, startupPrompt...) // stages the embedded docs and SDKs
	best := fixture.warmPrompt(ctx, t, fixture.cwd)
	t.Logf("cwd %s; first prompt with models.json (warm, min of 3): %d bytes, %d objects, %d GC cycles", fixture.cwd, best.AllocBytes, best.AllocObjects, best.GCCycles)
	if best.AllocBytes > budgetBytes || best.AllocObjects > budgetObjects {
		startupBudgetError(t, "a warm first prompt with a models.json allocated %d bytes in %d objects, budget %d bytes in %d objects", best.AllocBytes, best.AllocObjects, budgetBytes, budgetObjects)
	}
}

// Per directory level between the working directory and the root, Pi's startup makes ten existsSync probes and nothing else
// (with no git repository and no context file on the way): <dir>/.agents/skills twice for the project trust check
// (trust-manager.ts:196-207, run from main.ts:722 and :742) and once for skill discovery (package-manager.ts:2505-2519), <dir>/.git
// for the git root of that discovery (package-manager.ts:460-472) and for the worktree shadow check of context files
// (footer-data-provider.ts:16-48), and the five context file candidates (resource-loader.ts:184-203). The trust check also
// realpaths the working directory twice, an lstat per component. PiG makes the same probes and realpaths, each for the system
// call's copy of the path and no FileInfo, path string or error value, plus the one path string per level of the ancestor
// .agents/skills list that skill discovery returns. Its skill inputs and source infos share one walk (codingagent.AgentsSkillScan),
// and the session directory takes getcwd's physical path as it is. That is 13 objects per level, 2.3 KB over 32 levels (each copy
// is of a path that grows by a level's name, so the bytes per level grow with the depth). Before, a level cost 205-242 objects and
// 16-23 KB; with the probes cut but three skill walks, four trust realpaths without HOME and a session realpath, 22-23 objects.
const (
	startupLevelObjectsCeiling = 15
	startupLevelBytesCeiling   = 2_600
)

// Windows budgets (owner decision 2026-10-16, handoffs/owner-signoff.md). The startup walk probes the same files as Pi
// (resource-loader.ts:185), but on Windows every os.Stat, os.Lstat and environment read converts its path or key to UTF-16
// on the heap and builds a FileInfo: about five objects a probe against Linux's one or two. nodefs.Exists and the realpath
// walk already read attributes through a pooled buffer (72fe1f18a5), which took a level from 58 objects / 11,298 bytes to
// 32 / 6,561. The budgets are the GitHub windows-latest runner's measurements, the machine CI gates on, plus under 2%, so a
// Windows regression still fails; Linux and darwin keep theirs. Max of four CI runs (38874938533, 38881990325,
// 38889740946, 38903696084; the AWS fork, 8.3 and probe changes between them move these by under 1%): --version 808,320
// bytes / 5,689 objects; warm first prompt 2,869,072 / 18,596; with models.json 3,203,440 / 21,944; per level (38903696084)
// 6,465 / 32. The Windows VPS measured fewer objects (--version 4,199) than the runner; the runner is the reference.
const (
	windowsVersionBytes        = 824_000
	windowsVersionObjects      = 5_800
	windowsPromptBytes         = 2_924_000
	windowsPromptObjects       = 21_100
	windowsModelsPromptBytes   = 3_265_000
	windowsModelsPromptObjects = 25_200
	windowsLevelObjectsCeiling = 33
	windowsLevelBytesCeiling   = 6_690
)

// startupVersionRuns is how many `pig --version` runs the budget may measure to find one within budget. The Go runtime starts one more OS
// thread in some Windows process starts than in others: runtime.main locks the main goroutine to the main thread and blocks it in gcenable
// waiting for the GC workers, and when no idle M is parked yet schedule -> stoplockedm -> handoffp -> startm -> newm hands its P to a new M.
// That M costs 6 objects and 5,096 bytes (m, g0, gsignal and the profiling stack), whatever pig does. Under Wine on a Linux host, a Go program
// that only reads runtime.MemStats measured 190 objects / 47,616 bytes in 21 of 30 starts and 196 / 52,712 in the other 8; pig --version
// measured 756.6-756.8 KB or 761.6-761.9 KB the same way, with the same binary, and a heap profile put the doubled allocations under
// runtime.newm <- startm <- handoffp <- stoplockedm. The GitHub runner read 822,488 bytes at the 0.5.2 batch head and 827,800 at its merge,
// 5,312 apart, with no pig change that allocates at startup between them. The budget gates pig's own allocations, so a run over budget is
// measured again, up to this many times, and the test judges one whole run (never the minimum bytes of one run with the minimum objects of
// another). Noise passes in a later run; a regression is over budget in every run and fails with the smallest run's numbers.
const startupVersionRuns = 10

// startupLevelCeilingFor is the per-level budget of goos.
func startupLevelCeilingFor(goos string) (objects, bytes uint64) {
	if goos == "windows" {
		return windowsLevelObjectsCeiling, windowsLevelBytesCeiling
	}
	return startupLevelObjectsCeiling, startupLevelBytesCeiling
}

// startupLevels is how many levels deeper the second working directory is: run to run noise of several KB between two warm first
// prompts stays a few hundred bytes per level.
const startupLevels = 32

// startupLevelRuns is how many shallow and deep pairs the per-level budget may measure to find one within budget.
const startupLevelRuns = 10

func TestStartupAllocationPerDirectoryLevel(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	fixture := newStartupFixture(t)
	ctx := testbudget.Context(t)
	deep := fixture.cwd
	for level := range startupLevels {
		deep = filepath.Join(deep, fmt.Sprintf("l%02d", level))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// measuredEnvironment passes no HOME, so pig asks the system's user database for the home directory, and the per-level work depends on the answer: with no entry for the user (a container run as a uid its
		// passwd file does not list) there is no home, the user's ~/.agents/skills becomes the relative ".agents/skills", and every level's comparison against it resolves that path again, 17 objects a level
		// instead of 13. The fixture's home states the home directory, as it does for every user who has one. Windows always has one, and its budgets were measured without HOME.
		fixture.env = []string{"HOME=" + fixture.home}
	}
	fixture.measure(ctx, t, fixture.cwd, startupPrompt...) // stages the embedded docs and SDKs
	perLevel := func(shallow, deep uint64) uint64 {
		if deep < shallow {
			return 0
		}
		return (deep - shallow) / startupLevels
	}
	objectsCeiling, bytesCeiling := startupLevelCeilingFor(runtime.GOOS)
	// The bytes of two whole-process totals carry the runtime's own allocations: under CPU contention the scheduler starts extra OS threads, and each costs 6 objects and about 5,100 bytes (runtime.newm, see
	// startupVersionRuns), and a deep run makes more system calls to be caught in the middle of than a shallow one. Thirty deep runs on a loaded machine measured +0, +5.2-7.3 or +10.6-13.3 KB over the quiet
	// minimum, in steps of one thread; thirty quiet ones, under 2.2 KB: 2,300 bytes a level at rest, 2,666 once with two threads more in the deep runs. Like --version, the measurement is repeated
	// and one whole pair is judged: noise passes in a later pair, a regression (a level costing more) is over budget in every pair and fails with the smallest pair's numbers.
	type levelPair struct {
		shallow, deep  profiling.ProcessMetrics
		objects, bytes uint64
	}
	within := func(p levelPair) bool {
		return p.objects <= objectsCeiling && (p.bytes <= bytesCeiling || runtime.GOOS == "darwin")
	}
	var best levelPair
	for run := 1; run <= startupLevelRuns; run++ {
		pair := levelPair{shallow: fixture.warmPrompt(ctx, t, fixture.cwd), deep: fixture.warmPrompt(ctx, t, deep)}
		pair.objects, pair.bytes = perLevel(pair.shallow.AllocObjects, pair.deep.AllocObjects), perLevel(pair.shallow.AllocBytes, pair.deep.AllocBytes)
		t.Logf("run %d: warm first prompt from %s: %d bytes, %d objects; %d levels deeper: %d bytes, %d objects; per level %d bytes, %d objects", run, fixture.cwd, pair.shallow.AllocBytes, pair.shallow.AllocObjects, startupLevels, pair.deep.AllocBytes, pair.deep.AllocObjects, pair.bytes, pair.objects)
		if run == 1 || pair.bytes < best.bytes || (pair.bytes == best.bytes && pair.objects < best.objects) {
			best = pair
		}
		if within(pair) {
			best = pair
			break
		}
	}
	objects, bytes := best.objects, best.bytes
	if objects > objectsCeiling {
		startupBudgetError(t, "each directory level above the working directory costs a warm first prompt %d bytes in %d objects, ceiling %d bytes in %d objects", bytes, objects, bytesCeiling, objectsCeiling)
	}
	// darwin's per-level bytes swing 2,048-2,743 across runs of one build while its objects hold at 12-13 (8 runs at
	// a70a334ac4 on an M4 Pro): the difference of two whole-process totals divided by 32 carries the totals' noise. There the
	// byte bar is reported, not failed (owner decision 2026-10-16); the object bar, which measures the per-level work, blocks
	// everywhere, and the byte bar blocks on every other GOOS.
	if bytes > bytesCeiling {
		if runtime.GOOS == "darwin" {
			t.Logf("startup per-level bytes (report only on darwin): %d bytes per level, ceiling %d", bytes, bytesCeiling)
			fmt.Printf("::warning title=startup per-level bytes::%s: %d bytes per level, ceiling %d\n", t.Name(), bytes, bytesCeiling)
		} else {
			startupBudgetError(t, "each directory level above the working directory costs a warm first prompt %d bytes in %d objects, ceiling %d bytes in %d objects", bytes, objects, bytesCeiling, objectsCeiling)
		}
	}
}

var startupPrompt = []string{"--model", "test-faux/faux-1", "--no-extensions", "-p", "reply with exactly: ok"}

// startupFixture is the declared place of a startup measurement. Startup walks every directory from the working directory to the
// root, so the measurement moves with the depth of the working directory: under a host TMPDIR two levels deeper than /tmp, a warm
// first prompt measured 2,734,056 bytes instead of 2,675,184 (TestStartupAllocationPerDirectoryLevel gates that cost). The
// fixture's root is testenv.ShortTempDir, directly in /tmp whatever TMPDIR names, and holds the working directory (<root>/w,
// three components deep on Linux), PIG_HOME, the agent directory and the process's TMPDIR. On Windows ShortTempDir is in the
// host temp directory.
type startupFixture struct {
	binary, cwd, home, agentDir, tmp string
	// env is added to the measured environment.
	env []string
}

func newStartupFixture(t *testing.T) *startupFixture {
	t.Helper()
	root := testenv.ShortTempDir(t, "pig-startup-")
	f := &startupFixture{binary: buildPigBinaryForSignalTest(t), cwd: filepath.Join(root, "w"), home: filepath.Join(root, "home"), agentDir: filepath.Join(root, "agent"), tmp: filepath.Join(root, "tmp")}
	for _, dir := range []string{f.cwd, f.home, f.agentDir, f.tmp} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// measure runs pig with args from cwd and returns its PIG_PROFILE=metrics record.
func (f *startupFixture) measure(ctx context.Context, t *testing.T, cwd string, args ...string) profiling.ProcessMetrics {
	t.Helper()
	dir, err := os.MkdirTemp(f.tmp, "profile-")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, f.binary, args...)
	command.Dir = cwd
	command.Env = append(append(measuredEnvironment(os.Environ()), f.env...), "TMPDIR="+f.tmp, "PIG_HOME="+f.home, "PIG_CODING_AGENT_DIR="+f.agentDir, "PIG_PROFILE=metrics", "PIG_PROFILE_DIR="+dir, "GOMAXPROCS=1",
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pig %v: %v\n%s", args, err, output)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "pig-*-metrics.json"))
	if len(files) != 1 {
		t.Fatalf("want one metrics file, got %v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var stats profiling.ProcessMetrics
	if err := json.Unmarshal(data, &stats); err != nil || stats.AllocBytes == 0 || stats.AllocObjects == 0 {
		t.Fatalf("stats %q: %v", data, err)
	}
	return stats
}

// warmPrompt is the smallest of three first-prompt measurements from cwd, after a run has staged the embedded docs and SDKs.
func (f *startupFixture) warmPrompt(ctx context.Context, t *testing.T, cwd string) profiling.ProcessMetrics {
	t.Helper()
	best := f.measure(ctx, t, cwd, startupPrompt...)
	for range 2 {
		next := f.measure(ctx, t, cwd, startupPrompt...)
		best.AllocBytes = min(best.AllocBytes, next.AllocBytes)
		best.AllocObjects = min(best.AllocObjects, next.AllocObjects)
	}
	return best
}

// version measures pig --version from cwd and returns one whole run: the first whose bytes and objects are both within budget, else the run
// with the fewest bytes (its own objects with it), after startupVersionRuns runs. A run over budget is measured again because the Go runtime's
// start moves the count (see startupVersionRuns). Every run is logged, so the two modes stay visible in CI logs.
func (f *startupFixture) version(ctx context.Context, t *testing.T, cwd string, budget startupAllocBudget) profiling.ProcessMetrics {
	t.Helper()
	var best profiling.ProcessMetrics
	for run := 1; run <= startupVersionRuns; run++ {
		current := f.measure(ctx, t, cwd, "--version")
		t.Logf("pig --version run %d: %d bytes, %d objects", run, current.AllocBytes, current.AllocObjects)
		if run == 1 || current.AllocBytes < best.AllocBytes {
			best = current
		}
		if current.AllocBytes <= budget.versionBytes && current.AllocObjects <= budget.versionObjects {
			return current
		}
	}
	return best
}

// measuredEnvironment keeps only the variables a process needs to start on the host: the runtime copies and indexes every environment variable at start, so an inherited development or CI environment (a lane shell measured 100 more objects for pig --version than a clean one) would move the measurement away from the committed source it gates.
func measuredEnvironment(environ []string) []string {
	var kept []string
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "PATH", "SYSTEMROOT", "WINDIR", "TMPDIR", "TEMP", "TMP":
			kept = append(kept, entry)
		}
	}
	return kept
}

// startupBudgetError fails the test on a startup allocation budget.
func startupBudgetError(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Errorf(format, args...)
}
