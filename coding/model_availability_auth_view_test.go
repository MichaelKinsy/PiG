package coding

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

const (
	authViewOld = `{"anthropic":{"type":"api_key","key":"a-key"}}`
	authViewNew = `{"openai":{"type":"api_key","key":"o-key-rewritten"}}`
)

// authViewOutcome is what an availability pass published: the configured state of the two providers the rewrite swaps, and the providers with available models.
type authViewOutcome struct {
	Anthropic bool     `json:"anthropic"`
	OpenAI    bool     `json:"openai"`
	Available []string `json:"available"`
}

// authViewScenario is the pass during which auth.json was rewritten (During) and the pass after it (Next).
type authViewScenario struct {
	During authViewOutcome `json:"during"`
	Next   authViewOutcome `json:"next"`
}

type authViewOperation struct {
	Auth         int  `json:"auth"`
	Store        int  `json:"store"`
	OneMacrotask bool `json:"oneMacrotask"`
}

type piAuthViewReport struct {
	Restore         authViewOperation `json:"restore"`
	Pass            authViewOperation `json:"pass"`
	BeforeFirstRead authViewScenario  `json:"before-first-read"`
	AfterFirstRead  authViewScenario  `json:"after-first-read"`
	AfterThePass    authViewScenario  `json:"after-the-pass"`
	// Corrupt is the pass over an auth.json that does not parse: every credential read rejects, and Promise.all reports one rejection.
	Corrupt authViewRejection `json:"corrupt"`
}

type authViewRejection struct {
	Error    string `json:"error"`
	GetError string `json:"getError"`
}

// piAuthViewScript runs Pi 1.1.0's ModelRuntime over auth.json and models-store.json in agent. It counts the stats of both files in one warm
// catalog restore (models.refresh) and one warm availability pass (queueAvailabilityRefresh), and whether each operation's stats all ran in one
// macrotask: async_hooks counts every callback that is not a promise reaction or a nextTick. It then has another process rewrite auth.json
// before the pass's first stat, after its first stat, or after the pass, and reports what that pass and the next one publish.
const piAuthViewScript = `(async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const { createHook } = require('node:async_hooks');
  const { spawnSync } = require('node:child_process');
  const [root, agent, OLD, NEW] = process.argv.slice(1);
  const authPath = path.join(agent, 'auth.json');
  let macrotasks = 0;
  let quiet = false;
  const types = new Map();
  createHook({
    init(id, type) { types.set(id, type); },
    before(id) {
      const type = types.get(id);
      if (!quiet && type !== 'PROMISE' && type !== 'TickObject' && type !== 'Microtask') macrotasks++;
    },
    destroy(id) { types.delete(id); },
  }).enable();
  const stats = [];
  let pre = null;
  let post = null;
  const statSync = fs.statSync;
  fs.statSync = function (file, ...rest) {
    const name = typeof file === 'string' ? path.basename(file) : '';
    const watched = name === 'auth.json' || name === 'models-store.json';
    if (watched) {
      stats.push({ name, macrotasks });
      if (name === 'auth.json' && pre) pre();
    }
    const result = statSync.call(this, file, ...rest);
    if (name === 'auth.json' && post) post();
    return result;
  };
  require('node:module').syncBuiltinESMExports();
  const rewrite = (content) => {
    quiet = true;
    const child = spawnSync(process.execPath, ['-e', 'require("node:fs").writeFileSync(process.argv[1], process.argv[2])', authPath, content]);
    quiet = false;
    if (child.status !== 0) throw new Error('rewrite failed: ' + child.stderr);
  };
  const { ModelRuntime } = await import(require('node:url').pathToFileURL(path.join(root, 'dist', 'core', 'model-runtime.js')).href);
  fs.writeFileSync(authPath, OLD);
  const runtime = await ModelRuntime.create({ authPath, modelsPath: path.join(agent, 'models.json'), refreshOnCreate: false });
  const outcome = () => ({
    anthropic: runtime.hasConfiguredAuth('anthropic'),
    openai: runtime.hasConfiguredAuth('openai'),
    available: [...new Set(runtime.getAvailableSnapshot().map((model) => model.provider))].sort(),
  });
  const operation = async (run) => {
    const start = stats.length;
    await run();
    const list = stats.slice(start);
    return {
      auth: list.filter((stat) => stat.name === 'auth.json').length,
      store: list.filter((stat) => stat.name === 'models-store.json').length,
      oneMacrotask: new Set(list.map((stat) => stat.macrotasks)).size <= 1,
    };
  };
  await runtime.models.refresh({ allowNetwork: false });
  await runtime.queueAvailabilityRefresh();
  const report = {
    restore: await operation(() => runtime.models.refresh({ allowNetwork: false })),
    pass: await operation(() => runtime.queueAvailabilityRefresh()),
  };
  for (const when of ['before-first-read', 'after-first-read', 'after-the-pass']) {
    rewrite(OLD);
    await runtime.queueAvailabilityRefresh();
    let reads = 0;
    if (when === 'before-first-read') pre = () => { if (++reads === 1) rewrite(NEW); };
    if (when === 'after-first-read') post = () => { if (++reads === 1) rewrite(NEW); };
    await runtime.queueAvailabilityRefresh();
    pre = post = null;
    if (when === 'after-the-pass') rewrite(NEW);
    const during = outcome();
    await runtime.queueAvailabilityRefresh();
    report[when] = { during, next: outcome() };
  }
  fs.writeFileSync(authPath, '{"anthropic":');
  report.corrupt = { error: '', getError: '' };
  try {
    await runtime.queueAvailabilityRefresh();
  } catch (error) {
    report.corrupt.error = error instanceof Error ? error.message : String(error);
  }
  report.corrupt.getError = runtime.getError() ?? '';
  console.log(JSON.stringify(report));
})().catch((error) => { console.error(error); process.exit(1); });`

func runPiAuthView(t *testing.T) piAuthViewReport {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	agent := t.TempDir()
	cmd := exec.CommandContext(t.Context(), node, "-e", piAuthViewScript, root, agent, authViewOld, authViewNew)
	// No provider key or credential file reaches Pi from the environment.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "PI_OFFLINE=1"}
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
			stderr = exit.Stderr
		}
		t.Fatalf("Pi: %v\n%s\n%s", err, out, stderr)
	}
	var report piAuthViewReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &report); err != nil {
		t.Fatalf("Pi report %q: %v", out, err)
	}
	return report
}

// rewriteAround has another writer rewrite auth.json just before or just after the first credential read of the pass. Reads run one at a time, so
// "first" is well defined although the pass reads from many goroutines.
type rewriteAround struct {
	ai.CredentialStore
	mu            sync.Mutex
	reads         int
	before, after func()
}

func (s *rewriteAround) step(read func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.reads == 1 && s.before != nil {
		s.before()
	}
	read()
	if s.reads == 1 && s.after != nil {
		s.after()
	}
}

func (s *rewriteAround) Read(ctx context.Context, id string) (credential *ai.Credential, err error) {
	s.step(func() { credential, err = s.CredentialStore.Read(ctx, id) })
	return credential, err
}

func (s *rewriteAround) List(ctx context.Context) (infos []ai.CredentialInfo, err error) {
	s.step(func() { infos, err = s.CredentialStore.List(ctx) })
	return infos, err
}

func (s *rewriteAround) arm(before, after func()) {
	s.mu.Lock()
	s.reads, s.before, s.after = 0, before, after
	s.mu.Unlock()
}

// Q3 of lever-RegistryAuth: what an availability pass sees when another process rewrites auth.json between and during operations.
//
// Pi 1.1.0 (auth-storage.ts readLatestData) stats auth.json on every credential read. Its warm catalog restore and availability pass make every
// stat in one macrotask, so nothing in the process runs between those reads and a write by another process is ordered against them only by
// time: a write landing after the pass's last stat is a schedule Pi allows for any write that lands during the pass. Pig's pass reads auth.json
// through one view, so a write after its first read publishes exactly what Pi publishes when the write lands after the pass, and the next pass sees
// the write as Pi's next pass does. A write before the first read reaches the whole pass in both.
func TestAvailabilityPassAuthViewMatchesPiUpstream(t *testing.T) {
	pi := runPiAuthView(t)
	t.Logf("Pi warm catalog restore: %d auth.json and %d models-store.json stats; availability pass: %d auth.json stats", pi.Restore.Auth, pi.Restore.Store, pi.Pass.Auth)
	t.Logf("Pi, rewrite after the pass's first stat: %+v", pi.AfterFirstRead.During)
	if !pi.Restore.OneMacrotask || !pi.Pass.OneMacrotask {
		t.Fatalf("Pi ran a warm operation's stats across macrotasks (restore %+v, pass %+v): the one-view premise does not hold", pi.Restore, pi.Pass)
	}
	if pi.Pass.Auth < 2 {
		t.Fatalf("Pi's availability pass statted auth.json %d times", pi.Pass.Auth)
	}

	clearProviderEnvKeys(t)
	t.Setenv("HOME", t.TempDir())
	agent := t.TempDir()
	authPath := filepath.Join(agent, "auth.json")
	rewrite := func(content string) {
		if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
			t.Error(err)
		}
	}
	rewrite(authViewOld)
	auth, err := ai.NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	store := &rewriteAround{CredentialStore: auth}
	modelsPath := filepath.Join(agent, "models.json")
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{Credentials: store, ModelsPath: new(&modelsPath), ModelsStorePath: filepath.Join(agent, "models-store.json"), RefreshOnCreate: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	outcome := func() authViewOutcome {
		available := []string{}
		for _, model := range runtime.GetAvailableSnapshot() {
			if !slices.Contains(available, model.ProviderMeta.ProviderID) {
				available = append(available, model.ProviderMeta.ProviderID)
			}
		}
		slices.Sort(available)
		// Pi's hasConfiguredAuth reads snapshot.configuredProviders (model-runtime.ts:544-546), as Pig's does.
		return authViewOutcome{Anthropic: runtime.HasConfiguredAuth("anthropic"), OpenAI: runtime.HasConfiguredAuth("openai"), Available: available}
	}
	pass := func() {
		if err := runtime.queueAvailabilityRefresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	scenario := func(before, after, afterPass func()) authViewScenario {
		rewrite(authViewOld)
		pass()
		store.arm(before, after)
		pass()
		store.arm(nil, nil)
		if afterPass != nil {
			afterPass()
		}
		during := outcome()
		pass()
		return authViewScenario{During: during, Next: outcome()}
	}
	toNew := func() { rewrite(authViewNew) }
	for _, check := range []struct {
		name    string
		pig, pi authViewScenario
	}{
		{"rewrite before the first read", scenario(toNew, nil, nil), pi.BeforeFirstRead},
		{"rewrite after the first read", scenario(nil, toNew, nil), pi.AfterThePass},
		{"rewrite after the pass", scenario(nil, nil, toNew), pi.AfterThePass},
	} {
		if !reflect.DeepEqual(check.pig, check.pi) {
			t.Errorf("%s: Pig published %+v, Pi %+v", check.name, check.pig, check.pi)
		}
	}
	rewrite(`{"anthropic":`)
	var corrupt authViewRejection
	if err := runtime.queueAvailabilityRefresh(t.Context()); err != nil {
		corrupt.Error = err.Error()
	}
	corrupt.GetError = runtime.GetError()
	if corrupt != pi.Corrupt {
		t.Errorf("a pass over a corrupt auth.json: Pig %+v, Pi %+v", corrupt, pi.Corrupt)
	}
}
