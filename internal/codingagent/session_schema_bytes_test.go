package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi session-manager.ts:1259-1283 persists the projected tools without rebuilding their schemas. Compare the actual on-disk compaction declarations after append, load and a second compaction.
func TestCompactionToolSchemaBytesMatchPi(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
 import {readFileSync} from 'node:fs';
 import {SessionManager} from './extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/session-manager.js';
 import {createAllTools} from './extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/tools/index.js';
 import {toToolDeclaration} from './extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/utils/transcript.js';
 const session=SessionManager.create(process.argv[1],process.argv[1]);
 session.appendMessage({role:'system',content:'',toolsAdded:Object.values(createAllTools(process.argv[1])).map(toToolDeclaration),timestamp:1});
 const user=session.appendMessage({role:'user',content:'hello',timestamp:2});
 session.appendMessage({role:'assistant',content:[],api:'test',provider:'test',model:'test',usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:3});
 session.appendCompaction('summary',user,100,undefined,false);
 const entry=JSON.parse(readFileSync(session.getSessionFile(),'utf8').trimEnd().split('\n').at(-1));
 process.stdout.write(JSON.stringify(entry.systemMessage.toolsAdded));
 `, t.TempDir())
	cmd.Dir = root
	want, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pi compaction: %v: %s", err, want)
	}
	manager := tempSessionMgr(t)
	session, err := manager.Create("schema-bytes", "")
	if err != nil {
		t.Fatal(err)
	}
	declarations := []ai.ToolSchema{}
	for _, tool := range tools.CreateAllTools(t.TempDir(), nil, "") {
		declarations = append(declarations, ai.ToToolDeclaration(tool.Schema()))
	}
	if _, err := session.AppendMessage(agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), ToolsAdded: declarations, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	user, err := session.AppendMessage(mkUserMsg("hello"))
	if err != nil {
		t.Fatal(err)
	}
	flushSession(t, session)
	for range 2 {
		if _, err := session.AppendCompaction("summary", user, 100, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(session.Path())
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		var entry struct {
			SystemMessage struct {
				ToolsAdded json.RawMessage `json:"toolsAdded"`
			} `json:"systemMessage"`
		}
		if err := json.Unmarshal(lines[len(lines)-1], &entry); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(entry.SystemMessage.ToolsAdded, want) {
			t.Fatalf("persisted declarations:\n got %s\nwant %s", entry.SystemMessage.ToolsAdded, want)
		}
		session, err = manager.Load(session.Path())
		if err != nil {
			t.Fatal(err)
		}
	}
}
