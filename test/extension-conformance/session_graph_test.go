package extensionconformance

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/cli"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/sessiongraph"
)

// The session graph: the classes an extension constructs around a session. Each SDK runs the same script (testfixture/sessiongraph for Go, testdata/session-graph-rust, testdata/session_graph_python.py, and testdata/session-graph.mjs, where Node runs Pi's own SessionManager) against the real host and reports what it saw; every report must equal the literal table below. The table is Pi's behavior (session-manager.ts) as the Node row shows it, so an SDK that returns a constant, never calls the host, or builds the object differently fails its own row.

// The settings files of the script: the global file in the agent directory and the project file in the project's configuration directory.
const (
	sessionGraphGlobalSettings  = `{"defaultProvider":"anthropic","defaultModel":"opus","theme":"dark","steeringMode":"all","quietStartup":true,"externalEditor":"vim","enabledModels":["a*"],"compaction":{"enabled":false,"reserveTokens":1234},"retry":{"maxRetries":5},"packages":["npm:pkg-a",{"source":"npm:pkg-b","extensions":["x"]}],"unmodelled":{"kept":1}}`
	sessionGraphProjectSettings = `{"defaultModel":"sonnet","skills":["./s"],"compaction":{"reserveTokens":99}}`
)

//go:embed testdata/session_graph_python.py
var sessionGraphPython string

// sessionGraphSessionManagerWant is the report of Pi's own SessionManager (the Node row) for the script, with the run's directory tree as "<root>" and generated ids as "<uuid>".
//
//go:embed testdata/session-graph-session-manager.json
var sessionGraphSessionManagerWant string

type sessionGraphRig struct {
	t    *testing.T
	host *subprocess.Host
	ext  extension.Extension
}

func newSessionGraphRig(t *testing.T) *sessionGraphRig {
	t.Helper()
	r := &sessionGraphRig{t: t, host: subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())}
	t.Cleanup(func() { r.host.Shutdown("test complete") })
	r.host.SetSessionGraph(coding.NewSessionGraph())
	return r
}

// newSessionGraphRigWithChildren is the rig of a script that creates child agent sessions: the host also serves createAgentSession over the model runtime of its own services.
func newSessionGraphRigWithChildren(t *testing.T) *sessionGraphRig {
	t.Helper()
	r := newSessionGraphRig(t)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	bridge := subprocess.NewUIBridge(func() {})
	// A Node extension's own sessions stream through the host, as in the CLI (extensions.go).
	icodingagent.WireEarlyModelStream(bridge, "")
	r.host.SetUIBridge(bridge)
	cli.BindChildSessions(bridge, services)
	return r
}

func (r *sessionGraphRig) finish(ext extension.Extension) {
	r.t.Helper()
	r.ext = ext
	runner := inproc.NewRunner([]extension.Extension{ext}, r.t.TempDir(), r.host.Runtime())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
}

// script runs the fixture's tool in a directory tree this call owns and returns its report.
func (r *sessionGraphRig) script(tool string) (map[string]any, string) {
	r.t.Helper()
	root := r.t.TempDir()
	// The real path: the host resolves symlinks of its working directories on some systems, and the report names them.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		r.t.Fatal(err)
	}
	configDir := icodingagent.ConfigDirName()
	modelServer := sessionGraphModelServer(r.t)
	args := map[string]any{
		"mrAuth": filepath.Join(root, "mr", "auth.json"), "mrModels": filepath.Join(root, "mr", "models.json"), "mrStore": filepath.Join(root, "mr", "models-store.json"), "mrServer": modelServer,
		"cwd": filepath.Join(root, "project"), "dir": filepath.Join(root, "sessions"), "otherDir": filepath.Join(root, "other"), "junk": filepath.Join(root, "junk.jsonl"),
		"probeExt": filepath.Join(root, "probe", "probe.mjs"),
		"agentDir": filepath.Join(root, "agent"), "configDir": configDir, "root": root, "pmCwd": filepath.Join(root, "pm-project"), "pmAgentDir": filepath.Join(root, "pm-agent"), "loaderAgentDir": filepath.Join(root, "agent-loader"), "brokenCwd": filepath.Join(root, "broken"),
	}
	files := map[string]string{
		filepath.Join(root, "mr", "auth.json"):                                   `{"stored-prov":{"type":"api_key","key":"sk-stored"}}`,
		filepath.Join(root, "mr", "models.json"):                                 `{"providers":{"mr-local":{"baseUrl":"` + modelServer + `","api":"openai-completions","apiKey":"mr-key","models":[{"id":"m1","name":"M One","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":8000,"maxTokens":1000}]}}}`,
		filepath.Join(root, "agent", "settings.json"):                            sessionGraphGlobalSettings,
		filepath.Join(root, "project", configDir, "settings.json"):               sessionGraphProjectSettings,
		filepath.Join(root, "broken", configDir, "settings.json"):                "{bad",
		filepath.Join(root, "other", ".keep"):                                    "",
		filepath.Join(root, "project", "sub", ".keep"):                           "",
		filepath.Join(root, "project", configDir, "skills", ".keep"):             "",
		filepath.Join(root, "anc", ".agents", "skills", ".keep"):                 "",
		filepath.Join(root, "anc", "proj", ".keep"):                              "",
		filepath.Join(root, "project", configDir, "skills", "alpha", "SKILL.md"): "---\nname: alpha\ndescription: The alpha skill\n---\nAlpha body\n",
		filepath.Join(root, "project", configDir, "prompts", "hello.md"):         "---\ndescription: Say hello\nargument-hint: <name>\n---\nHello $1\n",
		filepath.Join(root, "project", "AGENTS.md"):                              "project context\n",
		filepath.Join(root, "project", configDir, "SYSTEM.md"):                   "project system\n",
		filepath.Join(root, "project", configDir, "APPEND_SYSTEM.md"):            "project append\n",
		filepath.Join(root, "extra-skills", "beta", "SKILL.md"):                  "---\nname: beta\ndescription: The beta skill\n---\nBeta body\n",
		filepath.Join(root, "extra-prompts", "bye.md"):                           "---\ndescription: Say bye\n---\nBye $1\n",
		filepath.Join(root, "probe", "probe.mjs"):                                childHandlesProbe,
		filepath.Join(root, "pm-project", ".keep"):                               "",
		filepath.Join(root, "pm-agent", ".keep"):                                 "",
		filepath.Join(root, "pkgs", "localpkg", "package.json"):                  `{"name":"localpkg","version":"1.0.0","pi":{"skills":["./skills"]}}`,
		filepath.Join(root, "pkgs", "localpkg", "skills", "gamma", "SKILL.md"):   "---\nname: gamma\ndescription: The gamma skill\n---\nGamma body\n",
		filepath.Join(root, "agent-loader", ".keep"):                             "",
		filepath.Join(root, "bare", ".keep"):                                     "",
		filepath.Join(root, "agent-array", "trust.json"):                         "[1]",
		filepath.Join(root, "agent-bad-json", "trust.json"):                      "{bad",
		filepath.Join(root, "agent-bad-value", "trust.json"):                     `{"/x":"yes"}`,
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	if err := os.WriteFile(args["junk"].(string), []byte("this is not a session\n"), 0o600); err != nil {
		r.t.Fatal(err)
	}
	def, ok := r.ext.Tools[tool]
	if !ok {
		r.t.Fatalf("the fixture has no tool %q (tools: %v)", tool, r.ext.ToolOrder)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		r.t.Fatal(err)
	}
	result, err := def.Definition.Execute(r.t.Context(), "call-"+tool, encoded, nil)
	if err != nil {
		r.t.Fatalf("%s: %v", tool, err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &report); err != nil {
		r.t.Fatalf("%s = %q: %v", tool, result.Text(), err)
	}
	return report, root
}

type sessionGraphLoader func(t *testing.T, r *sessionGraphRig)

func sessionGraphSDKs() map[string]sessionGraphLoader {
	return map[string]sessionGraphLoader{
		"go-fused": func(t *testing.T, r *sessionGraphRig) {
			ext, err := r.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: sessiongraph.Name, Path: "/ext/" + sessiongraph.Name + ".go", Enabled: true}, sessiongraph.Extension().RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			r.finish(*ext)
		},
		"go-strict": func(t *testing.T, r *sessionGraphRig) {
			root := findModuleRoot(t)
			cfg := subprocess.ExtConfig{
				Name: sessiongraph.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/session-graph-go"), Enabled: true, Isolation: "strict",
				RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
				SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/session-graph-go", Package: "example.com/session-graph-go",
			}
			r.load(t, cfg)
		},
		"rust": func(t *testing.T, r *sessionGraphRig) {
			source, err := filepath.Abs("testdata/session-graph-rust")
			if err != nil {
				t.Fatal(err)
			}
			cfg, _, err := subprocess.ResolveExtConfigWithIdentity(source, sessiongraph.Name)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Isolation = "isolated"
			r.load(t, cfg)
		},
		"python": func(t *testing.T, r *sessionGraphRig) {
			cfg := packedFlagFactory(t, findModuleRoot(t), "python", sessiongraph.Name, false)
			cfg.Isolation = "strict"
			cfg.ContentHash = "python-session-graph-" + fmt.Sprint(len(sessionGraphPython))
			if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(sessionGraphPython), 0o600); err != nil {
				t.Fatal(err)
			}
			r.load(t, cfg)
		},
		"node": func(t *testing.T, r *sessionGraphRig) {
			source, err := filepath.Abs("testdata/session-graph.mjs")
			if err != nil {
				t.Fatal(err)
			}
			cfg, _, err := subprocess.ResolveExtConfigWithIdentity(source, sessiongraph.Name)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Isolation = "isolated"
			r.load(t, cfg)
		},
	}
}

func (r *sessionGraphRig) load(t *testing.T, cfg subprocess.ExtConfig) {
	t.Helper()
	loaded, failures := r.host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	r.finish(loaded[0])
}

func eachSessionGraphSDK(t *testing.T, body func(t *testing.T, r *sessionGraphRig, sdkName string)) {
	t.Helper()
	eachSessionGraphSDKWith(t, newSessionGraphRig, body)
}

// eachSessionGraphSDKWith runs body for every SDK on the rig newRig builds.
func eachSessionGraphSDKWith(t *testing.T, newRig func(*testing.T) *sessionGraphRig, body func(t *testing.T, r *sessionGraphRig, sdkName string)) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts SDK subprocesses)")
	}
	loaders := sessionGraphSDKs()
	names := make([]string, 0, len(loaders))
	for name := range loaders {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			loaders[name](t, r)
			body(t, r, name)
		})
	}
}

var (
	sessionGraphUUID   = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	sessionGraphServer = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`)
)

// normalize writes a report the way every SDK must produce it: the directory tree of the run is "<root>", a generated session id is "<uuid>", and a path separator is "/". The tables are Pi's report on a POSIX host; a run on Windows names its paths with the platform's separator (path.join and path.relative of Pi's own classes do too, and so do the Go, Python and Rust hosts' paths), and no table holds a backslash.
func normalizeSessionGraph(t *testing.T, report map[string]any, root string) map[string]any {
	t.Helper()
	return normalizeSessionGraphFor(report, root, filepath.Separator == '\\')
}

// normalizeSessionGraphFor is normalizeSessionGraph for a platform whose path separator is a backslash or a slash, in the names of members as well as in values (a report keys its answers by path).
func normalizeSessionGraphFor(report map[string]any, root string, backslash bool) map[string]any {
	if backslash {
		root = strings.ReplaceAll(root, `\`, "/")
	}
	text := func(s string) string {
		if backslash {
			s = strings.ReplaceAll(s, `\`, "/")
		}
		return sessionGraphServer.ReplaceAllString(sessionGraphUUID.ReplaceAllString(strings.ReplaceAll(s, root, "<root>"), "<uuid>"), "<server>")
	}
	var walk func(any) any
	walk = func(value any) any {
		switch v := value.(type) {
		case string:
			return text(v)
		case []any:
			out := make([]any, len(v))
			for i, item := range v {
				out[i] = walk(item)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(v))
			for name, item := range v {
				out[text(name)] = walk(item)
			}
			return out
		}
		return value
	}
	return walk(report).(map[string]any)
}

// A Windows report names its tree with backslashes, in values and in member names and in paths relative to the tree; a POSIX report keeps a backslash it holds.
func TestNormalizeSessionGraphWindowsPaths(t *testing.T) {
	windowsRoot := `D:\a\_temp\pig extension acceptance\run\004`
	report := map[string]any{
		"cwd":    windowsRoot + `\project`,
		"list":   []any{map[string]any{"cwd": windowsRoot + `\project`, "id": "0b1c2d3e-4f50-4162-8394-a5b6c7d8e9f0", "n": float64(1), "ok": true, "none": nil}},
		"source": `..\..\pkgs\localpkg`,
		"file":   map[string]any{windowsRoot + `\anc`: true, windowsRoot + `\sibling`: false},
		"server": "http://127.0.0.1:41234/v1",
	}
	want := map[string]any{
		"cwd":    "<root>/project",
		"list":   []any{map[string]any{"cwd": "<root>/project", "id": "<uuid>", "n": float64(1), "ok": true, "none": nil}},
		"source": "../../pkgs/localpkg",
		"file":   map[string]any{"<root>/anc": true, "<root>/sibling": false},
		"server": "<server>/v1",
	}
	if got := normalizeSessionGraphFor(report, windowsRoot, true); !reflect.DeepEqual(got, want) {
		t.Errorf("windows report = %v, want %v", got, want)
	}
	posix := normalizeSessionGraphFor(map[string]any{"cwd": "/tmp/run/004/project", "pattern": `a\b`}, "/tmp/run/004", false)
	if wantPosix := map[string]any{"cwd": "<root>/project", "pattern": `a\b`}; !reflect.DeepEqual(posix, wantPosix) {
		t.Errorf("posix report = %v, want %v", posix, wantPosix)
	}
}

// diffSessionGraph lists the members at which got differs from want, by path.
func diffSessionGraph(path string, got, want any, out *[]string) {
	gotMap, gotIsMap := got.(map[string]any)
	wantMap, wantIsMap := want.(map[string]any)
	if gotIsMap && wantIsMap {
		keys := map[string]bool{}
		for key := range gotMap {
			keys[key] = true
		}
		for key := range wantMap {
			keys[key] = true
		}
		for key := range keys {
			diffSessionGraph(path+"."+key, gotMap[key], wantMap[key], out)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		*out = append(*out, fmt.Sprintf("%s:\n got  %s\n want %s", path, g, w))
	}
}

// requireSessionGraphReport fails with the differing members when got is not want.
func requireSessionGraphReport(t *testing.T, got map[string]any, want string) {
	t.Helper()
	var expected map[string]any
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	var diffs []string
	diffSessionGraph("", got, expected, &diffs)
	sort.Strings(diffs)
	for _, diff := range diffs {
		t.Error(diff)
	}
}

// TestSessionGraphSessionManagerAcrossSDKs is session-manager.ts SessionManager (create, open, continueRecent, inMemory, forkFrom, findById, list, listAll and the log methods) for every SDK.
func TestSessionGraphSessionManagerAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("session_manager")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphSessionManagerWant)
	})
}

// settings-manager.ts SettingsManager (create, inMemory, every getter and setter, flush, reload, drainErrors, project trust) for every SDK.
func TestSessionGraphSettingsManagerAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("settings_manager")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphSettingsManagerWant)
	})
}

// sessionGraphSettingsManagerWant is the report of Pi's own SettingsManager (the Node row).
//
//go:embed testdata/session-graph-settings-manager.json
var sessionGraphSettingsManagerWant string

// trust-manager.ts ProjectTrustStore (get, getEntry, set, setMany, the store's file and its errors) and hasTrustRequiringProjectResources for every SDK.
func TestSessionGraphProjectTrustStoreAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("project_trust_store")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphProjectTrustStoreWant)
	})
}

// sessionGraphProjectTrustStoreWant is the report of Pi's own ProjectTrustStore (the Node row).
//
//go:embed testdata/session-graph-project-trust-store.json
var sessionGraphProjectTrustStoreWant string

// resource-loader.ts DefaultResourceLoader (options, results, overrides as callbacks, extendResources, settings handle, resolveProjectTrust) for every SDK. The options extensionFactories and extensionsOverride are not in the row: the native SDKs refuse them (gap SdkSessionGraph).
func TestSessionGraphDefaultResourceLoaderAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("resource_loader")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphDefaultResourceLoaderWant)
	})
}

// sessionGraphDefaultResourceLoaderWant is the report of Pi's own DefaultResourceLoader (the Node row).
//
//go:embed testdata/session-graph-default-resource-loader.json
var sessionGraphDefaultResourceLoaderWant string

// package-manager.ts DefaultPackageManager (settings sources, listing, resolve with a missing-source callback, install and remove of a local package with progress events, temporary sources, updates) for every SDK.
func TestSessionGraphDefaultPackageManagerAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("package_manager")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphDefaultPackageManagerWant)
	})
}

// sessionGraphDefaultPackageManagerWant is the report of Pi's own DefaultPackageManager (the Node row).
//
//go:embed testdata/session-graph-default-package-manager.json
var sessionGraphDefaultPackageManagerWant string

// sessionGraphModelServer is an OpenAI-compatible chat completions endpoint that answers every request with the stream "hello".
func sessionGraphModelServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"role":"assistant","content":"hel"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// model-runtime.ts ModelRuntime (create over its own credential and model files, catalog and auth reads, runtime keys, provider and virtual model registration, refresh, availability, streams, classify, generateImages, login and logout) for every SDK.
func TestSessionGraphModelRuntimeAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("model_runtime")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphModelRuntimeWant)
	})
}

// sessionGraphModelRuntimeWant is the report of Pi's own ModelRuntime (the Node row), with the run's model server as "<server>".
//
//go:embed testdata/session-graph-model-runtime.json
var sessionGraphModelRuntimeWant string

// childHandlesProbe is the extension the child session of the child_handles script loads (additionalExtensionPaths of its resource loader): it registers one tool, which the session must have among its active tools.
const childHandlesProbe = `export default function (pi) {
  pi.registerTool({ name: "probe_tool", label: "probe", description: "the probe extension's tool", parameters: { type: "object" }, execute: async () => ({ content: [{ type: "text", text: "probed" }], details: {} }) });
}
`

// createAgentSession over the objects an extension built (sdk.ts: sessionManager, settingsManager, resourceLoader, modelRuntime, scopedModels) for every SDK, against the report of Pi's own createAgentSession run (the Node row): the model, thinking level and settings come from the objects, the transcript lands in the extension's session manager, the appendix is the loader's, the extension of the loader reaches the session as a tool, and the objects the extension let go of keep serving the session.
func TestSessionGraphChildHandlesAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDKWith(t, newSessionGraphRigWithChildren, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("child_handles")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphChildHandlesWant)
	})
}

// sessionGraphChildHandlesWant is the report of Pi's own createAgentSession (the Node row).
//
//go:embed testdata/session-graph-child-handles.json
var sessionGraphChildHandlesWant string
