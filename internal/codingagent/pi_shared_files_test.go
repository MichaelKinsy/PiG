package codingagent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func piDirectoryNode(t *testing.T, script string, args ...string) *exec.Cmd {
	t.Helper()
	root, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	prelude := `import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
import {join} from 'node:path';
import {readFileSync} from 'node:fs';
const root = process.argv[1];
assert.equal(JSON.parse(readFileSync(join(root,'package.json'))).version, '1.0.0');
const load = name => import(pathToFileURL(join(root,'dist',name+'.js')).href);
const {AuthStorage, ReadOnlyAuthStorage} = await load('core/auth-storage');
const {SettingsManager} = await load('core/settings-manager');
const {SessionManager} = await load('core/session-manager');
const {ModelConfig} = await load('core/model-config');
const {ProjectTrustStore} = await load('core/trust-manager');
const {FileModelsStore} = await load('core/models-store');
`
	return exec.CommandContext(t.Context(), node, append([]string{"--input-type=module", "--eval", prelude + script, root}, args...)...)
}

// Real Pi 0.87.1 reads PiG's writes and writes back into the same files.
// SettingsManager preserves unknown keys (settings-manager.ts:547-593),
// AuthStorage uses flat credentials (auth-storage.ts:472-498), and sessions
// preserve the same append-only tree (session-manager.ts:1202-1207).
func TestPiSharedFilesRoundTrip(t *testing.T) {
	t.Setenv("PIG_USE_PI_DIRS", "1")
	agentDir, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"theme":"dark","pigOnly":{"retained":true}}`)
	writeSettingsFixture(t, filepath.Join(cwd, ".pi", "settings.json"), `{}`)
	settings := NewSettingsManager(cwd, AgentDir())
	if err := settings.SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetProjectPackages([]PackageSource{{Source: "./shared"}}); err != nil {
		t.Fatal(err)
	}
	auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("pig", ai.Credential{Type: ai.CredentialAPIKey, Key: "$KEY", Env: map[string]string{"KEY": "scoped"}, Extra: map[string]json.RawMessage{"pigOnly": json.RawMessage(`true`)}}); err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("expired", ai.Credential{Type: ai.CredentialOAuth}); err != nil {
		t.Fatal(err)
	}
	modelData := `{"providers":{"shared":{"baseUrl":"http://localhost:1/v1","api":"openai-completions","apiKey":"unused","models":[{"id":"shared-model","name":"Shared","reasoning":false,"input":["text"],"contextWindow":12345,"maxTokens":678}]}}}`
	writeSettingsFixture(t, filepath.Join(agentDir, "models.json"), modelData)
	manager := NewSessionManager(cwd)
	session, err := manager.Create("shared-roundtrip", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "from Pig"}}, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "Pig answer"}}, API: "openai-completions", Provider: "shared", ModelID: "shared-model", StopReason: ai.StopReasonStop, Timestamp: 2}}); err != nil {
		t.Fatal(err)
	}
	cmd := piDirectoryNode(t, `
const [agent, cwd, sessionFile] = process.argv.slice(2);
const settings = SettingsManager.create(cwd, agent);
assert.equal(settings.getTheme(), 'light');
assert.deepEqual(settings.getProjectSettings().packages, ['./shared']);
assert.deepEqual(settings.getGlobalSettings().pigOnly, {retained:true});
settings.setTheme('dark'); settings.setProjectPackages(['./pi-package']);
await settings.flush(); assert.deepEqual(settings.drainErrors(), []);
const auth = AuthStorage.create(join(agent,'auth.json'));
assert.deepEqual(await auth.read('pig'), {type:'api_key',key:'scoped',env:{KEY:'scoped'},pigOnly:true});
assert.deepEqual(await new ReadOnlyAuthStorage(join(agent,'auth.json')).read('expired'), {type:'oauth',access:'',refresh:'',expires:0});
await auth.modify('pi', async () => ({type:'oauth',access:'pi-access',refresh:'pi-refresh',expires:42,extensionField:{keep:true}}));
const model = await ModelConfig.load(join(agent,'models.json'));
assert.equal(model.getError(), undefined);
assert.equal(model.getProvider('shared').models[0].contextWindow,12345);
const session = SessionManager.open(sessionFile);
assert.deepEqual(session.buildSessionContext().messages.map(m => m.content[0]?.text ?? m.content), ['from Pig','Pig answer']);
session.appendMessage({role:'user',content:[{type:'text',text:'from Pi'}],timestamp:3});
session.appendSessionInfo('Shared by Pi');
assert((await SessionManager.list(cwd)).some(s => s.path === sessionFile));
console.log('Pi read settings/auth/models/session; Pi wrote settings/auth/session');
`, agentDir, cwd, session.Path())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Pi round trip: %v\n%s", err, output)
	} else {
		t.Log(strings.TrimSpace(string(output)))
	}
	settings.Reload()
	if settings.Get().Theme != "dark" || settings.GetProjectSettings().Packages[0].Source != "./pi-package" {
		t.Fatalf("Pi settings not loaded: %+v", settings.Get())
	}
	cred, ok, err := auth.GetRaw("pi")
	var extra map[string]bool
	if decodeErr := json.Unmarshal(cred.Extra["extensionField"], &extra); decodeErr != nil || !extra["keep"] {
		t.Fatalf("provider field lost: %s, %v", cred.Extra["extensionField"], decodeErr)
	}
	if err != nil || !ok || cred.Access != "pi-access" {
		t.Fatalf("Pi auth not loaded: %+v, %v", cred, err)
	}
	if err := auth.Set("other", ai.Credential{Type: ai.CredentialAPIKey, Key: "other"}); err != nil {
		t.Fatal(err)
	}
	verifyAuth := piDirectoryNode(t, `
const store = new ReadOnlyAuthStorage(process.argv[2]);
assert.deepEqual(await store.read('pi'), {type:'oauth',access:'pi-access',refresh:'pi-refresh',expires:42,extensionField:{keep:true}});
assert.equal((await store.read('other')).key,'other');
`, auth.Path())
	if output, err := verifyAuth.CombinedOutput(); err != nil {
		t.Fatalf("Pi rereads preserved provider fields: %v\n%s", err, output)
	}
	registry := NewModelRegistry(agentDir)
	entry, found := registry.Resolve("shared", "shared-model")
	if registry.LoadError() != "" || !found || entry.ContextWindow != 12345 {
		t.Fatalf("shared models: %+v, %s", entry, registry.LoadError())
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if err != nil || string(data) != modelData {
		t.Fatalf("read-only models.json changed: %s, %v", data, err)
	}
	reloaded, err := manager.Load(session.Path())
	if err != nil {
		t.Fatal(err)
	}
	messages := reloaded.BuildContext(nil)
	if len(messages) != 3 || messages[2].User == nil || !strings.Contains(fmt.Sprint(messages[2].User.Content), "from Pi") || reloaded.GetSessionName() != "Shared by Pi" {
		t.Fatalf("Pi session not loaded: %+v / %s", messages, reloaded.GetSessionName())
	}
}

// Both runtimes modify the same auth/settings/trust files concurrently. Every
// requested key must survive, not merely the last valid JSON document.
func TestPiSharedFilesConcurrentWriters(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Pi's proper-lockfile retries only EEXIST. On Windows, a lock directory PiG is releasing is delete-pending, and
		// Node's mkdir on it fails EPERM, so Pi's writer aborts mid-run. That is Node's behavior; PiG's lock retries the
		// delete-pending state (internal/pilock delete_pending_windows_test.go), and the single-writer tests cover Windows.
		t.Skip("Pi's lock does not retry a delete-pending lock directory on Windows")
	}
	agentDir, cwd := t.TempDir(), t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"pigOnly":{"retained":true}}`)
	auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	trust := NewProjectTrustStore(agentDir)
	models := ai.NewFileModelsStore(filepath.Join(agentDir, "models-store.json"))
	writeSettingsFixture(t, models.Path(), `{}`)
	const operations = 32
	cmd := piDirectoryNode(t, `
const [agent, cwd, count] = process.argv.slice(2);
const auth = AuthStorage.create(join(agent,'auth.json'));
const settings = SettingsManager.create(cwd, agent, {projectTrusted:false});
const trust = new ProjectTrustStore(agent);
const models = new FileModelsStore(join(agent,'models-store.json'));
console.log('ready');
await new Promise(resolve => process.stdin.once('data', resolve));
process.stdin.pause();
for (let i=0;i<Number(count);i++) {
  await auth.modify('pi-'+i, async () => ({type:'api_key',key:'pi-key-'+i}));
  settings.setEditorPaddingX(i % 3); await settings.flush();
  assert.deepEqual(settings.drainErrors(), []);
  trust.set(join(cwd,'pi-'+i), true);
  await models.write('pi-'+i,{models:[{id:'pi-model-'+i}],etag:'pi-etag-'+i});
}
console.log('done');
`, agentDir, cwd, fmt.Sprint(operations))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	// exec's copier goroutine owns stderr until Wait returns, so a failing run reaps the child before reading it.
	reapedStderr := func() string {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return stderr.String()
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatalf("Pi failed to start: %s", reapedStderr())
	}
	if _, err := stdin.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	for i := range operations {
		_, err := auth.Modify(t.Context(), fmt.Sprintf("pig-%d", i), func(*ai.Credential) (*ai.Credential, error) {
			return &ai.Credential{Type: ai.CredentialAPIKey, Key: fmt.Sprintf("pig-key-%d", i)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := settings.SetTheme(fmt.Sprintf("pig-theme-%d", i)); err != nil {
			t.Fatal(err)
		}
		if err := trust.Set(filepath.Join(cwd, fmt.Sprintf("pig-%d", i)), new(true)); err != nil {
			t.Fatal(err)
		}
		if err := models.Write(t.Context(), fmt.Sprintf("pig-%d", i), ai.ModelsStoreEntry{Models: []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"id":"pig-model-%d"}`, i))}, ETag: fmt.Sprintf("pig-etag-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if !scanner.Scan() || scanner.Text() != "done" {
		t.Fatalf("Pi writes failed: %s", reapedStderr())
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Pi writes: %v\n%s", err, stderr.String())
	}
	creds, err := auth.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"pi", "pig"} {
		for i := range operations {
			key := fmt.Sprintf("%s-%d", side, i)
			if creds[key].Key != fmt.Sprintf("%s-key-%d", side, i) {
				t.Errorf("lost %s credential: %+v", key, creds[key])
			}
			entry, err := models.Read(t.Context(), key)
			if err != nil || entry == nil || entry.ETag != fmt.Sprintf("%s-etag-%d", side, i) {
				t.Fatalf("lost %s catalog: %+v, %v", key, entry, err)
			}
			decision, err := trust.Get(filepath.Join(cwd, key))
			if err != nil || decision == nil || !*decision {
				t.Fatalf("lost %s trust: %v", key, err)
			}
		}
	}
	var persisted struct {
		Theme          string          `json:"theme"`
		EditorPaddingX int             `json:"editorPaddingX"`
		PigOnly        map[string]bool `json:"pigOnly"`
	}
	data, err := os.ReadFile(settings.GlobalPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Theme != fmt.Sprintf("pig-theme-%d", operations-1) || persisted.EditorPaddingX != (operations-1)%3 || !persisted.PigOnly["retained"] {
		t.Fatalf("lost settings update: %s", data)
	}
	verifyModels := piDirectoryNode(t, `
const store = new FileModelsStore(process.argv[2]);
for(let i=0;i<Number(process.argv[3]);i++) {
  assert.deepEqual(await store.read('pig-'+i), {models:[{id:'pig-model-'+i}],etag:'pig-etag-'+i});
}
`, models.Path(), fmt.Sprint(operations))
	if output, err := verifyModels.CombinedOutput(); err != nil {
		t.Fatalf("Pi reads PiG's dynamic catalogs: %v\n%s", err, output)
	}
	for _, file := range []string{"auth.json", "settings.json", "trust.json", "models-store.json"} {
		if _, err := os.Stat(filepath.Join(agentDir, file) + ".lock"); !os.IsNotExist(err) {
			t.Errorf("%s lock remains: %v", file, err)
		}
	}
}
