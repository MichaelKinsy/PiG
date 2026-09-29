package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Exercise the pinned published Pi implementation, not a copied model of its rules.
// interactive-mode.ts:309-321; agent-session-runtime.ts:312-316; tui/markdown.ts:606.
func TestW3SessionPiComparison(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	cmd := piDirectoryNode(t, `
const {formatResumeCommand} = await load('modes/interactive/interactive-mode');
const {AgentSessionRuntime} = await load('core/agent-session-runtime');
const {createRequire} = await import('node:module');
const require=createRequire(join(root,'package.json'));
const {Markdown} = await import(pathToFileURL(require.resolve('@earendil-works/pi-tui')).href);
Object.defineProperty(process.stdout, 'isTTY', {value:true});
const cwd=process.argv[2];
const resumes=[];
for (const custom of [false,true]) {
 const sm=SessionManager.create(cwd,custom ? join(cwd,'custom dir') : undefined);
 sm.appendMessage({role:'assistant',content:[{type:'text',text:'saved'}],stopReason:'stop',timestamp:1});
 const reopened=SessionManager.open(sm.getSessionFile(), sm.getSessionDir());
 resumes.push({path:reopened.getSessionFile(),id:reopened.getSessionId(),dir:reopened.getSessionDir(),command:formatResumeCommand(reopened)});
}
const sm=SessionManager.create(cwd);
const leaf=sm.appendThinkingLevelChange('off');
const runtime=Object.create(AgentSessionRuntime.prototype);
runtime._session={sessionManager:sm,sessionFile:sm.getSessionFile()};
runtime.emitBeforeFork=async()=>({cancelled:false});
let error='';
try { await runtime.fork(leaf,{position:'at'}); } catch(e) { error=e.message; }
const theme=new Proxy({}, {get:()=>text=>text});
// Compare the complete component, including its final width padding.
const rules=[1,40,79,80,81,120].map(width=>new Markdown('---',0,0,theme).render(width));
process.stdout.write(JSON.stringify({resumes,error,rules}));
`, cwd)
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pi: %v\n%s", err, data)
	}
	var oracle struct {
		Resumes []struct{ Path, ID, Dir, Command string }
		Error   string
		Rules   [][]string
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatalf("Pi output %s: %v", data, err)
	}
	for _, row := range oracle.Resumes {
		resolvedDir, _ := filepath.Abs(row.Dir)
		defaultDir, _ := filepath.Abs(defaultSessionDir(cwd))
		if got := resumeCommand(row.Path, row.ID, row.Dir, row.Dir != "" && resolvedDir != defaultDir); got != "pig"+strings.TrimPrefix(row.Command, "pi") {
			t.Errorf("resume: got %q Pi %q", got, row.Command)
		}
	}
	sm := NewSessionManager(cwd)
	sess, err := sm.Create("unsaved", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.CheckSavedForFork(); err == nil || err.Error() != oracle.Error {
		t.Errorf("unsaved: %v Pi %q", err, oracle.Error)
	}
	if _, err := os.Stat(sess.Path()); !os.IsNotExist(err) {
		t.Errorf("unsaved file exists: %v", err)
	}
	for i, width := range []int{1, 40, 79, 80, 81, 120} {
		got := tui.NewMarkdown("---").Render(width)
		for j := range got {
			got[j] = stripANSITest(got[j])
		}
		if !slices.Equal(got, oracle.Rules[i]) {
			t.Errorf("rule width %d: got %q Pi %q", width, got, oracle.Rules[i])
		}
	}
}
