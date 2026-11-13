# /llama catalog sync: which 15-second timer ends a refresh that hangs

## Question

`syncCatalog` (`packages/coding-agent/src/extensions/llama/index.ts:46-64`) bounds the catalog list and the registry refresh with one `AbortSignal.timeout(15_000)` (`:51`). Every request made under it adds a 15-second timeout of its own (`extensions/llama/client.ts:193-194`). When the llama.cpp server does not answer, the user sees one of two texts in the connection error view: `Model catalog refresh timed out.` (`index.ts:60`, when the refresh result is aborted) or `Could not connect to the server.` (`index.ts:17-20`, for an error whose name or message contains `timeout`). Which text Pi gives, and whether it can give either, decides what PiG must give.

## Runs

All runs are on one Linux host, each run in its own process, all runs of one case started together. Both sides use the same listener (below). The listener accepts every connection and answers by case:

| Case | The listener answers | The listener holds |
|---|---|---|
| never answers | nothing | every request |
| list hangs | everything but `GET /models` | every `GET /models` |
| refresh list hangs | the first `GET /models` (the list of `syncCatalog`) | every later request (the list of the refresh) |
| refresh props hang | every `GET /models` | every other request (`GET /props` of the refresh) |

### Pi

The `/llama` handler that the extension registers runs unchanged from the oracle build of the pinned commit `b4afdddf44e0`, on the Node release of `.node-version`. `ctx.modelRegistry` is Pi's `ModelRegistry` over Pi's `ModelRuntime`. Only the two prompts of the view are answered by the probe, and the text given to the connection error prompt is the result.

| Case | Runs | Text |
|---|---:|---|
| never answers | 20 of 20 | `Could not connect to the server.` |
| list hangs | 20 of 20 | `Could not connect to the server.` |
| refresh list hangs | 20 of 20 | `Model catalog refresh timed out.` |
| refresh props hang | 20 of 20 | `Model catalog refresh timed out.` |

Pi gives one text in each case. Five more runs of the third case recorded each `AbortSignal.timeout`: three are created (the one of `syncCatalog`, then one for each request) and they fire in creation order, 5 of 5.

The single text comes from that order, not from `index.ts:60`. In ten runs of the third case the timer of `syncCatalog` got 500 ms more, so that the timer of the refresh's request fired first, a state Pi does not reach by itself. All ten gave `Could not connect to the server.`. The refresh result is aborted only when the signal of `syncCatalog` itself is aborted (`packages/ai/src/models.ts:575`, `:639`, and `core/model-runtime.ts:925`). A request's own timeout that ends the refresh first is the error of the refresh (`models.ts:617-623`, thrown at `index.ts:61-62`).

### PiG in real time

The product is unchanged and no test clock is used. Two levels were run with `go1.27.4`:

- `commandSession.syncCatalog` with pi-ai's `Models` as the registry, the fixture of `TestLlamaCommandSyncCatalogEndsAtTheSharedFifteenSecondTimeout`.
- `llama.RunCommand` with the registry a Session gives the command (`ModelRegistry.RefreshModelRuntime`) and the real view. The text is read from what the view renders.

| Case | `syncCatalog` | `/llama` command | Text |
|---|---:|---:|---|
| never answers | 50 of 50 | 50 of 50 | `Could not connect to the server.` |
| list hangs | 50 of 50 | 50 of 50 | `Could not connect to the server.` |
| refresh list hangs | 50 of 50 | 50 of 50 | `Model catalog refresh timed out.` |
| refresh props hang | 50 of 50 | 50 of 50 | `Model catalog refresh timed out.` |
| catalog passed in (`index.ts:179`), refresh list hangs | 50 of 50 | not run | `Model catalog refresh timed out.` |

PiG gives Pi's text in all nine cases, 450 of 450 runs.

## Why a test clock showed the other text

The refresh signal reports the deadline of `syncCatalog` (`ai/models_runtime_signal.go`), and `context.WithDeadline` starts no timer under a parent deadline that is earlier. On a clock that advances between the start of the sync and its request, the request therefore has no timer of its own. In a `testing/synctest` bubble no time passes between the two, the deadlines are equal, Go starts the second timer, and two timers that are due at the same instant fire in either order. `TestLlamaCommandSyncCatalogEndsAtTheSharedFifteenSecondTimeout` then failed in about one run of ten with the request's timeout as the error of the sync.

The rule that follows from Pi's order: a request starts its own 15-second timer only when its caller has no deadline or a later one (`internal/codingagent/llama/fetch.go`, `requestContext`). `TestLlamaRequestLeavesTheTimeoutToACallerWhoseDeadlineIsDueNoLater` holds the two callers of that path to it.

Not run: a platform whose clock does not advance between the two calls. Only there could the product have given the other text outside a test.

## Probe sources

The Go probes are compiled into their packages with `go test -overlay` and are not part of the tree. They read the listener's address from `PROBE_URL`, as `http://127.0.0.1:<port>/<case>/<run id>`.

The listener:

```js
// The one listener both sides use. The path prefix /<mode>/<id> selects what a client id gets:
//   ok            every request is answered
//   hang-all      the connection is accepted and no request is ever answered
//   hang-list     GET /models is never answered; every other request is answered
//   hang-refresh  the first GET /models of an id is answered (the sync's own list); every later request is never answered
//   hang-props    every GET /models is answered; every other request is never answered (what the ledger's test holds: the refresh's /props)
import { createServer } from "node:http";
const answered = new Set();
const catalog = JSON.stringify({ data: [{ id: "qwen", status: { value: "loaded" }, meta: { n_ctx: 32768 } }] });
const server = createServer((req, res) => {
	const match = /^\/(ok|hang-all|hang-list|hang-refresh|hang-props)\/([^/]+)(\/[^?]*)/.exec(req.url);
	if (!match) {
		res.writeHead(404);
		res.end();
		return;
	}
	const [, mode, id, path] = match;
	const log = (what) => console.error(`${Date.now()} ${mode} ${id} ${req.method} ${path} ${what}`);
	const answer = () => {
		log("answered");
		res.writeHead(200, { "Content-Type": "application/json" });
		res.end(path === "/models" ? catalog : "{}");
	};
	if (mode === "ok") return answer();
	if (mode === "hang-all") return log("held");
	if (mode === "hang-props") return path === "/models" ? answer() : log("held");
	if (mode === "hang-list") return path === "/models" ? log("held") : answer();
	if (path === "/models" && !answered.has(id)) {
		answered.add(id);
		return answer();
	}
	log("held");
});
server.requestTimeout = 0;
server.headersTimeout = 0;
server.listen(0, "127.0.0.1", () => console.log(server.address().port));
```

The Pi probe (`node pi_probe.mjs <oracle package root>/ <url>`; `PROBE_TRACE=1` records the timers, `PROBE_SWAP=1 PROBE_SWAP_MS=500` delays the timer of `syncCatalog`):

```js
// Pi's /llama command (extensions/llama/index.ts, the pinned oracle build) against a listener that never answers. The command
// runs unchanged with Pi's ModelRuntime behind Pi's ModelRegistry as ctx.modelRegistry; only the view's two prompts are answered here, and
// the text the connection-error prompt is given is the result.
// PROBE_TRACE=1 records when each AbortSignal.timeout is created and fires. PROBE_SWAP=1 is the counterfactual Pi cannot produce itself:
// the first timer created (syncCatalog's) is given PROBE_SWAP_MS more (default 50), so a request's own timer fires first.
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const [root, url] = process.argv.slice(2);
const load = (path) => import(pathToFileURL(root + path).href);
const dir = mkdtempSync(join(tmpdir(), "ac4-pi-"));
process.env.PI_CODING_AGENT_DIR = dir;
delete process.env.LLAMA_BASE_URL;
delete process.env.LLAMA_API_KEY;
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, theme } = await load("dist/modes/interactive/theme/theme.js");
const { AuthStorage } = await load("dist/core/auth-storage.js");
const { ModelRuntime } = await load("dist/core/model-runtime.js");
const { ModelRegistry } = await load("dist/core/model-registry.js");
const llamaExtension = (await load("dist/extensions/llama/index.js")).default;
const credentials = AuthStorage.create(join(dir, "auth.json"));
await credentials.modify("llama.cpp", async () => ({ type: "api_key", env: { LLAMA_BASE_URL: url } }));
const runtime = await ModelRuntime.create({ credentials, modelsPath: null, modelsStorePath: join(dir, "models-store.json"), refreshOnCreate: false });
let command;
llamaExtension({
	registerProvider: (provider) => runtime.registerNativeProvider(provider),
	registerCommand: (name, options) => {
		if (name === "llama") command = options;
	},
});
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark");
const keybindings = new KeybindingsManager({});
setKeybindings(keybindings);
const texts = [];
const notices = [];
let finish;
const finished = new Promise((resolve) => {
	finish = resolve;
});
const ctx = {
	mode: "tui",
	modelRegistry: new ModelRegistry(runtime),
	ui: {
		notify: (message, type) => notices.push(`${type ?? "info"}: ${message}`),
		custom: (factory) => {
			const view = factory({ requestRender() {} }, theme, keybindings, () => finish());
			view.connectionError = async (_serverUrl, message) => {
				texts.push(message);
				return "close";
			};
			view.showModels = async () => {
				texts.push("<the model list>");
				return { type: "close" };
			};
			return finished;
		},
	},
};
const trace = [];
const tracing = process.env.PROBE_TRACE === "1";
const swapping = process.env.PROBE_SWAP === "1";
if (tracing || swapping) {
	// Installed immediately before the command runs, so index 0 is syncCatalog's own timer (index.ts:51).
	const original = AbortSignal.timeout.bind(AbortSignal);
	let count = 0;
	AbortSignal.timeout = (ms) => {
		const index = count++;
		const delay = swapping && index === 0 ? ms + Number(process.env.PROBE_SWAP_MS ?? 50) : ms;
		const signal = original(delay);
		trace.push(["created", index, delay, Math.round(performance.now() * 1000) / 1000]);
		signal.addEventListener("abort", () => trace.push(["fired", index, delay, Math.round(performance.now() * 1000) / 1000]));
		return signal;
	};
}
const started = performance.now();
await command.handler("", ctx);
const ms = Math.round(performance.now() - started);
if (tracing || swapping) await new Promise((resolve) => setTimeout(resolve, 300));
console.log(`PROBE-TEXT user=${JSON.stringify(texts.join(" / "))} notices=${JSON.stringify(notices)}`);
console.log(`PROBE-MS ${ms}`);
if (tracing || swapping) console.log(`PROBE-TRACE ${JSON.stringify(trace)}`);
process.exit(0);
```

The probe of `syncCatalog`, in package `internal/codingagent/llama`:

```go
package llama

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Probe, compiled in with -overlay and never committed: syncCatalog in real time (no synctest bubble) with the
// fixture's registry (pi-ai's Models), against a listener that never answers.
func TestZZRealtimeProbe(t *testing.T) {
	url := os.Getenv("PROBE_URL")
	if url == "" {
		t.Skip("no PROBE_URL")
	}
	provided := os.Getenv("PROBE_PROVIDED") == "1"
	session, _ := commandFixture(t, url)
	start := time.Now()
	var err error
	if provided {
		_, err = session.syncCatalog([]LlamaModelInfo{model("qwen", LlamaModelStatusLoaded)}, true)
	} else {
		_, err = session.syncCatalog(nil, false)
	}
	elapsed := time.Since(start)
	raw, user := "<nil>", "<the model list>"
	if err != nil {
		raw, user = err.Error(), connectionErrorMessage(err)
	}
	fmt.Printf("PROBE-TEXT user=%q raw=%q\nPROBE-MS %d\n", user, raw, elapsed.Milliseconds())
}
```

The probe of the command, in package `internal/codingagent`:

```go
//go:build !pig_strip_llama_cpp

package codingagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
)

type probeRegistry struct{ registry *ModelRegistry }

func (p probeRegistry) GetProviderAuth(ctx context.Context, id string) (*ai.AuthResult, error) {
	return p.registry.GetProviderAuth(ctx, id)
}

// Refresh is what coding.ModelRuntime.Refresh calls for the Session's registry (coding/model_runtime_native.go).
func (p probeRegistry) Refresh(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return p.registry.RefreshModelRuntime(ctx, options)
}

// Probe, compiled in with -overlay and never committed: the /llama command (llama.RunCommand) in real time with
// the registry a Session gives it and the real view; the text is read from what the view renders.
func TestZZRealtimeProbeProduction(t *testing.T) {
	url := os.Getenv("PROBE_URL")
	if url == "" {
		t.Skip("no PROBE_URL")
	}
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(auth)
	registry.SetModelsStore(ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")))
	controller := llama.CreateLlamaProvider()
	if err := registry.RegisterNativeModelsProvider(controller.ModelsProvider()); err != nil {
		t.Fatal(err)
	}
	if err := auth.Set(llama.LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": url}}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var notices []string
	shown, elapsed := "<nothing shown in 40 s>", time.Duration(0)
	start := time.Now()
	ctx := llama.CommandContext{
		Ctx: context.Background(), Mode: "tui",
		Notify: func(message, kind string) {
			mu.Lock()
			notices = append(notices, kind+": "+message)
			mu.Unlock()
		},
		Custom: func(factory func(requestRender, done func()) llama.CustomComponent) {
			done := make(chan struct{})
			view := factory(func() {}, func() { close(done) })
			deadline := time.After(40 * time.Second)
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			captured := false
			for {
				select {
				case <-done:
					return
				case <-deadline:
					return
				case <-tick.C:
					if captured {
						continue
					}
					text := stripANSI(strings.Join(view.Render(200), "\n"))
					if !strings.Contains(text, "llama.cpp unavailable") && !strings.Contains(text, "qwen") {
						continue
					}
					captured = true
					elapsed = time.Since(start)
					var parts []string
					for line := range strings.SplitSeq(strings.ReplaceAll(text, url, "<url>"), "\n") {
						// Only the lines with a letter, in ASCII: the view's rules are box-drawing runs that a byte cut in the job's summary would split.
						line = strings.TrimSpace(strings.Map(func(r rune) rune {
							if r > 126 {
								return -1
							}
							return r
						}, line))
						if strings.ContainsAny(line, "abcdefghijklmnopqrstuvwxyz") {
							parts = append(parts, line)
						}
					}
					shown = strings.Join(parts, " | ")
					view.HandleInput("\x1b")
				}
			}
		},
	}
	if err := llama.RunCommand(ctx, probeRegistry{registry}, controller); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	fmt.Printf("PROBE-TEXT shown=%q notices=%q\nPROBE-MS %d\n", shown, notices, elapsed.Milliseconds())
}
```
