package extensionconformance

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
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
	t    testing.TB
	host *subprocess.Host
	ext  extension.Extension
}

func newSessionGraphRig(t testing.TB) *sessionGraphRig {
	t.Helper()
	r := &sessionGraphRig{t: t, host: subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())}
	t.Cleanup(func() { r.host.Shutdown("test complete") })
	r.host.SetSessionGraph(coding.NewSessionGraph())
	return r
}

// newSessionGraphRigWithChildren is the rig of a script that creates child agent sessions: the host also serves createAgentSession over the model runtime of its own services.
func newSessionGraphRigWithChildren(t testing.TB) *sessionGraphRig {
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
	return r.scriptWith(tool, nil)
}

// scriptWith is script with the extra arguments added to the script's own.
func (r *sessionGraphRig) scriptWith(tool string, extra map[string]any) (map[string]any, string) {
	r.t.Helper()
	root := r.t.TempDir()
	// The real path: the host resolves symlinks of its working directories on some systems, and the report names them.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		r.t.Fatal(err)
	}
	configDir := icodingagent.ConfigDirName()
	modelServer := sessionGraphModelServer(r.t)
	toolServer := sessionGraphToolModelServer(r.t, "mcp__demo__echo", `{"text":"hi"}`)
	args := map[string]any{
		"mrToolModels": filepath.Join(root, "mr", "models-tool.json"), "mcpProbe": filepath.Join(root, "probe", "mcp.mjs"),
		"mrAuth": filepath.Join(root, "mr", "auth.json"), "mrModels": filepath.Join(root, "mr", "models.json"), "mrStore": filepath.Join(root, "mr", "models-store.json"), "mrServer": modelServer,
		"cwd": filepath.Join(root, "project"), "dir": filepath.Join(root, "sessions"), "otherDir": filepath.Join(root, "other"), "junk": filepath.Join(root, "junk.jsonl"),
		"probeExt": filepath.Join(root, "probe", "probe.mjs"), "runnerProbeA": filepath.Join(root, "probe", "runner-a.mjs"), "runnerProbeB": filepath.Join(root, "probe", "runner-b.mjs"), "servicesProbe": filepath.Join(root, "probe", "services.mjs"), "runtimeProbe": filepath.Join(root, "probe", "runtime.mjs"), "refsProbe": filepath.Join(root, "probe", "refs.mjs"), "servicesAgentDir": filepath.Join(root, "agent-services"),
		"agentDir": filepath.Join(root, "agent"), "configDir": configDir, "root": root, "pmCwd": filepath.Join(root, "pm-project"), "pmAgentDir": filepath.Join(root, "pm-agent"), "loaderAgentDir": filepath.Join(root, "agent-loader"), "brokenCwd": filepath.Join(root, "broken"),
	}
	maps.Copy(args, extra)
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
		filepath.Join(root, "probe", "services.mjs"):                             servicesProbe,
		filepath.Join(root, "probe", "refs.mjs"):                                 extensionRefsProbe,
		filepath.Join(root, "probe", "mcp.mjs"):                                  mcpOptionsProbe,
		filepath.Join(root, "mr", "models-tool.json"):                            `{"providers":{"mr-tool":{"baseUrl":"` + toolServer + `","api":"openai-completions","apiKey":"mr-key","models":[{"id":"m1","name":"M One","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":8000,"maxTokens":1000}]}}}`,
		filepath.Join(root, "probe", "runtime.mjs"):                              agentSessionRuntimeProbe,
		filepath.Join(root, "probe", "runner-a.mjs"):                             extensionRunnerProbeA,
		filepath.Join(root, "probe", "runner-b.mjs"):                             extensionRunnerProbeB,
		filepath.Join(root, "agent-services", "auth.json"):                       `{"svc-local":{"type":"api_key","key":"svc-key"}}`,
		filepath.Join(root, "agent-services", "models.json"):                     `{"providers":{"svc-local":{"baseUrl":"` + modelServer + `","api":"openai-completions","apiKey":"svc-key","models":[{"id":"s1","name":"S One","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4000,"maxTokens":500}]}}}`,
		filepath.Join(root, "agent-services", "settings.json"):                   `{"defaultProvider":"svc-local","defaultModel":"s1"}`,
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

type sessionGraphLoader func(t testing.TB, r *sessionGraphRig)

func sessionGraphSDKs() map[string]sessionGraphLoader {
	return map[string]sessionGraphLoader{
		"go-fused": func(t testing.TB, r *sessionGraphRig) {
			ext, err := r.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: sessiongraph.Name, Path: "/ext/" + sessiongraph.Name + ".go", Enabled: true}, sessiongraph.Extension().RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			r.finish(*ext)
		},
		"go-strict": func(t testing.TB, r *sessionGraphRig) {
			root := findModuleRoot(t)
			cfg := subprocess.ExtConfig{
				Name: sessiongraph.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/session-graph-go"), Enabled: true, Isolation: "strict",
				RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
				SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/session-graph-go", Package: "example.com/session-graph-go",
			}
			r.load(t, cfg)
		},
		"rust": func(t testing.TB, r *sessionGraphRig) {
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
		"python": func(t testing.TB, r *sessionGraphRig) {
			cfg := packedFlagFactory(t, findModuleRoot(t), "python", sessiongraph.Name, false)
			cfg.Isolation = "strict"
			cfg.ContentHash = "python-session-graph-" + fmt.Sprint(len(sessionGraphPython))
			if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(sessionGraphPython), 0o600); err != nil {
				t.Fatal(err)
			}
			r.load(t, cfg)
		},
		"lisp": func(t testing.TB, r *sessionGraphRig) {
			r.load(t, sessionGraphLispConfig(t))
		},
		"node": func(t testing.TB, r *sessionGraphRig) {
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

// sessionGraphLispConfig is the Lisp session-graph fixture (testdata/session-graph-lisp: the ASDF system "session-graph", one file of scripts per lane).
func sessionGraphLispConfig(t testing.TB) subprocess.ExtConfig {
	t.Helper()
	cfg := lispFixtureConfig(t, "session-graph-lisp", sessiongraph.Name)
	cfg.Package = "session-graph"
	return cfg
}

func (r *sessionGraphRig) load(t testing.TB, cfg subprocess.ExtConfig) {
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
func eachSessionGraphSDKWith(t *testing.T, newRig func(testing.TB) *sessionGraphRig, body func(t *testing.T, r *sessionGraphRig, sdkName string)) {
	t.Helper()
	eachSessionGraphCellWith(t, newRig, sessionGraphSDKs(), body)
}

// eachSessionGraphPackedWith runs body for every packed cell (a shared cell holding a second extension of the SDK's language) on the rig newRig builds: a packed cell must report what the isolated cell of the same SDK reports. A row's packed cells are a test of their own so that the isolated row's shard is not made longer by them.
func eachSessionGraphPackedWith(t *testing.T, newRig func(testing.TB) *sessionGraphRig, body func(t *testing.T, r *sessionGraphRig, sdkName string)) {
	t.Helper()
	eachSessionGraphCellWith(t, newRig, sessionGraphPackedSDKs(), body)
}

func eachSessionGraphCellWith(t *testing.T, newRig func(testing.TB) *sessionGraphRig, loaders map[string]sessionGraphLoader, body func(t *testing.T, r *sessionGraphRig, sdkName string)) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts SDK subprocesses)")
	}
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
func sessionGraphModelServer(t testing.TB) string {
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

// model-registry.ts ModelRegistry over a ModelRuntime (`new ModelRegistry(runtime)`: catalog, auth and request auth, provider registration by name and by Provider object, the Provider object members, virtual models, refresh, streams, classify and generateImages) for every SDK. The expectation is the report of Pi's own ModelRegistry (the Node row), except `builtin`: Pi's getProvider and getProviders answer a builtin provider's
// Provider object, and the Go, Python and Rust SDKs fail with the D78 error (docs/parity/divergences/D78.md), which their cells must report.
func TestSessionGraphModelRegistryAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, sdkName string) {
		report, root := r.script("model_registry")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		want := sessionGraphModelRegistryWant
		if sdkName != "node" {
			var expected map[string]any
			if err := json.Unmarshal([]byte(want), &expected); err != nil {
				t.Fatal(err)
			}
			const d78 = "error: builtin/composed Provider object carrier is unavailable (D78)"
			expected["builtin"] = map[string]any{"provider": d78, "providers": d78}
			encoded, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			want = string(encoded)
		}
		requireSessionGraphReport(t, got, want)
	})
}

// sessionGraphModelRegistryWant is the report of Pi's own ModelRegistry (the Node row), with the run's model server as "<server>".
//
//go:embed testdata/session-graph-model-registry.json
var sessionGraphModelRegistryWant string

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

// servicesProbe is the extension the services script loads (additionalExtensionPaths of the services' resource loader): it registers two flags and a provider while it loads, which createAgentSessionServices applies to the model runtime, and records in the session log what its session_start event and the flags carried (ctx.sessionManager.appendCustomEntry writes the log and emits no entry_appended event; pi.appendEntry also emits it, which a child session does not forward yet).
const servicesProbe = `export default function (pi) {
  pi.registerFlag("probe-flag", { type: "boolean", description: "probe" });
  pi.registerFlag("probe-text", { type: "string", description: "probe text" });
  pi.registerProvider("svc-prov", {
    baseUrl: "http://127.0.0.1:1/v1", apiKey: "k", api: "openai-completions",
    models: [{ id: "p1", name: "P One", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4000, maxTokens: 500 }],
  });
  pi.on("session_start", async (event, ctx) => {
    ctx.sessionManager.appendCustomEntry("probe_start", { reason: event.reason, previous: event.previousSessionFile ?? null, flag: pi.getFlag("probe-flag") ?? null, text: pi.getFlag("probe-text") ?? null });
  });
}
`

// createAgentSessionServices and createAgentSessionFromServices (agent-session-services.ts) for every SDK, against the report of Pi's own functions (the Node row): the resolved paths, the registrations and flag values of the extension the services' loader loaded (applied to the model runtime and reported as diagnostics), the services built over the agent directory's own files, and a session whose extensions see the sessionStartEvent.
func TestSessionGraphAgentSessionServicesAcrossSDKs(t *testing.T) {
	// Not parallel: the agent directory is the process environment's, which the host resolves a missing agentDir to and every SDK reads back as its host info.
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	newRig := func(t testing.TB) *sessionGraphRig {
		r := newSessionGraphRigWithChildren(t)
		r.host.SetHostInfo(subprocess.HostInfo{AgentDir: agentDir, ConfigDirName: ".pig", Version: coding.UpstreamPackageVersion})
		return r
	}
	eachSessionGraphSDKWith(t, newRig, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("services")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphServicesWant)
	})
}

// sessionGraphServicesWant is the report of Pi's own createAgentSessionServices and createAgentSessionFromServices (the Node row).
//
//go:embed testdata/session-graph-services.json
var sessionGraphServicesWant string

// pi-ai's createProvider (models.ts:1077) for every SDK, against the report of Pi's own createProvider run (the Node row): the provider's metadata and model lists, dispatch on model.api (and the stream error of a model whose api has no
// implementation), the two forms of `api`, the required-implementation error, deferred fetch and cancel, image and classifier operations, and the model refresh (stored restore, offline, aborted, refused and fetched publications).
func TestSessionGraphCreateProviderAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("create_provider")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphCreateProviderWant)
	})
}

// sessionGraphCreateProviderWant is the report of Pi's own createProvider (the Node row).
//
//go:embed testdata/session-graph-create-provider.json
var sessionGraphCreateProviderWant string

// extensionRefsProbe is the extension the extension_refs script loads next to the compiled-in extensions: when a session starts it records, in the session log, which of the codemode, tool_search and MCP tools and commands the session has, whether they are active, whether
// the codemode description declares the `models` namespace, and the parameter schemas.
const extensionRefsProbe = `export default function (pi) {
  pi.on("session_start", async (_event, ctx) => {
    const active = pi.getActiveTools();
    const tools = pi.getAllTools().filter((tool) => ["codemode", "tool_search"].includes(tool.name));
    ctx.sessionManager.appendCustomEntry("refs_seen", {
      tools: tools.map((tool) => ({ name: tool.name, active: active.includes(tool.name), models: tool.description.includes(String.fromCharCode(96) + "models" + String.fromCharCode(96) + ":"), parameters: tool.parameters, guidelines: tool.promptGuidelines ?? null })),
      commands: pi.getCommands().map((command) => command.name).filter((name) => name === "mcp"),
    });
  });
}
`

// createCodemodeExtension, createToolSearchExtension and createMcpExtension (extensions/*/index.ts) for every SDK, against the report of Pi's own factories (the Node row): a loader given the compiled-in extensions through extensionFactories lets a session
// that allows the tools list them (inactive by default, active when the tools option names them), with the codemode `models` option honored and the `/mcp` command registered, and a loader without them has none.
func TestSessionGraphExtensionFactoriesAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDKWith(t, newSessionGraphRigWithChildren, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("extension_refs")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphExtensionFactoriesWant)
	})
}

// sessionGraphExtensionFactoriesWant is the report of Pi's own extension factories (the Node row).
//
//go:embed testdata/session-graph-extension-factories.json
var sessionGraphExtensionFactoriesWant string

// sessionGraphToolModelServer is a model that calls the tool toolName with arguments on its first request and answers with text once a tool result is in the conversation.
func sessionGraphToolModelServer(t testing.TB, toolName, arguments string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		toolResult := false
		for _, message := range body.Messages {
			toolResult = toolResult || message.Role == "tool"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"role":"assistant","content":"done"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		}
		if !toolResult {
			call, _ := json.Marshal(map[string]any{"name": toolName, "arguments": arguments})
			chunks = []string{
				`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":` + string(call) + `}]},"finish_reason":null}]}`,
				`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
			}
		}
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// mcpOptionsProbe is the extension the mcp_options script loads next to the MCP extension: when the run ends it records, in the session log, the MCP tools the session has.
const mcpOptionsProbe = `export default function (pi) {
  pi.on("agent_end", async (_event, ctx) => {
    ctx.sessionManager.appendCustomEntry("mcp_seen", { tools: pi.getAllTools().map((tool) => tool.name).filter((name) => name.startsWith("mcp__")).sort(), active: pi.getActiveTools().filter((name) => name.startsWith("mcp__")).sort() });
  });
}
`

// createMcpExtension's function options (extensions/mcp/index.ts McpExtensionOptions) for every SDK, against the report of Pi's own factory (the Node row): loadConfig's context and result, the transports createTransport made (what the client sent them, in order, the auth provider's token), the credential store's operations, and a tool call that crosses the transport.
func TestSessionGraphMcpOptionsAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDKWith(t, newSessionGraphRigWithChildren, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("mcp_options")
		got := normalizeSessionGraph(t, report, root)
		if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
			encoded, _ := json.MarshalIndent(got, "", "\t")
			_ = os.WriteFile(path, encoded, 0o600)
		}
		requireSessionGraphReport(t, got, sessionGraphMcpOptionsWant)
	})
}

// sessionGraphMcpOptionsWant is the report of Pi's own createMcpExtension (the Node row).
//
//go:embed testdata/session-graph-mcp-options.json
var sessionGraphMcpOptionsWant string
