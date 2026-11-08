// Ports test/p2/agent.test.ts (Pi main c23b9ec1): code mode's one-shot `agent` tool (fake provider): parallel agents,
// validated JSON answers with retries, no reports to the main conversation, listing by nested call ID, and read-only
// inspection; the tool stays installed with code mode off.

package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

const agentScript = `
const schema = { type: "object", required: ["n"], properties: { n: { type: "number" } } };
const results = await Promise.all([1, 2].map((n) => tools.agent({ prompt: 'json {"n":' + n + '}', schema, tools: [] })));
let error = "";
try {
	await tools.agent({ prompt: 'json {"n":"x"}', schema, tools: [] });
} catch (caught) {
	error = caught.message;
}
return { results, error };
`

const agentDescription = "Run a one-shot subagent in your cwd and resolve to its final answer, parsed as JSON and validated against `schema` (a JSON Schema object). Rejects when the subagent fails, or when its answer still does not validate after 3 attempts. Its answer is not reported to you, only what the script returns. Run many at once with `Promise.all`."

var scriptHeader = regexp.MustCompile(`^Script completed\nWall time [\d.]+ seconds\nOutput:\n\n?`)

// scriptOutput is a code mode result's text after its "Script completed" header (Durable's coding-agent layout).
func scriptOutput(t *testing.T, text string) string {
	t.Helper()
	match := scriptHeader.FindString(text)
	if match == "" {
		t.Fatalf("a completed script's result: %s", text)
	}
	return text[len(match):]
}

func codemodeResult(view delta.JsonValue) *delta.JsonObject {
	results := toolResults(view, "codemode")
	if len(results) == 0 {
		return nil
	}
	return results[0]
}

// sectionsOf are the prompt sections in effect at the end of a conversation: its system messages replayed in order.
func sectionsOf(view delta.JsonValue) map[string]string {
	current := map[string]string{}
	for _, message := range messages(view) {
		if message.Value("role") != "system" {
			continue
		}
		sections := object(message.Value("sections"))
		if sections == nil {
			continue
		}
		for key, value := range sections.All() {
			if text, ok := value.(string); ok {
				current[key] = text
			} else {
				delete(current, key)
			}
		}
	}
	return current
}

func TestCodeModeAgentFakeProvider(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	var callID string
	t.Run("runs parallel one-shot agents with validated JSON, retries, and no reports", func(t *testing.T) {
		f.send("codemode " + agentScript)
		view := f.until(f.Chat.Member("view"), func(v delta.JsonValue) bool { return codemodeResult(v) != nil && idle(v) }, 0)
		result := codemodeResult(view)
		callID, _ = result.Value("toolCallId").(string)
		text := agentResultText(result)
		if result.Value("isError") != false {
			t.Fatal(text)
		}
		var value struct {
			Results []map[string]float64 `json:"results"`
			Error   string               `json:"error"`
		}
		decode(t, scriptOutput(t, text), &value)
		if len(value.Results) != 2 || value.Results[0]["n"] != 1 || value.Results[1]["n"] != 2 {
			t.Errorf("results = %v", value.Results)
		}
		// The first answer is JSON of the wrong shape, the retries' answers are not JSON at all.
		if !regexp.MustCompile(`not usable after 3 attempts: it is not JSON`).MatchString(value.Error) {
			t.Errorf("error = %q", value.Error)
		}
		for _, message := range messages(view) {
			if message.Value("role") == "user" && strings.Contains(marshal(t, message.Value("content")), `kind=\"subagent\"`) {
				t.Error("answers are not reported to the main conversation")
			}
		}
	})
	t.Run("lists each one-shot agent by its nested call ID and keeps it read-only", func(t *testing.T) {
		browser, err := p2test.HoldIn(f.Owner, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		children := f.until(browser.Member("children"), func(v delta.JsonValue) bool { return len(list(object(v).Value(f.Session))) == 3 })
		agents := list(object(children).Value(f.Session))
		var names []string
		for _, each := range agents {
			agent := object(each)
			callId, _ := agent.Value("callId").(string)
			if agent.Value("kind") != "agent" || !strings.HasPrefix(callId, callID+"/c") || agent.Value("lastEntryId") != float64(0) || len(list(agent.Value("tools"))) != 0 {
				t.Errorf("agent = %v", agent)
			}
			names = append(names, agent.Value("name").(string))
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{`json {"n":"x"}`, `json {"n":1}`, `json {"n":2}`}) {
			t.Fatalf("names = %q", names)
		}
		var first *delta.JsonObject
		for _, each := range agents {
			if object(each).Value("name") == `json {"n":1}` {
				first = object(each)
			}
		}
		// Read-only: it can be opened and read, not prompted, reconfigured or archived.
		sub := f.hold(first.Value("conversationId").(float64))
		f.until(sub.Member("view"), func(v delta.JsonValue) bool {
			return strings.Contains(marshal(t, object(v).Value("entries")), `{\"n\":1}`)
		})
		_, err = sub.Call("send", delta.JsonObjectOf("text", "hello", "requestId", "steer"), p2test.Background)
		if p2test.ExpectChord(t, err, data.CodeForbidden); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("send: %v", err)
		}
		_, err = sub.Call("configure", delta.JsonObjectOf("thinkingLevel", "off"), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeForbidden)
		control, err := p2test.HoldIn(f.Owner, subagentscontract.SubagentControl, delta.JsonObjectOf("session", f.Session), "participant")
		if err != nil {
			t.Fatal(err)
		}
		_, err = control.Call("archive", delta.JsonObjectOf("conversation", first.Value("conversationId"), "archived", true), p2test.Background)
		if p2test.ExpectChord(t, err, data.CodeForbidden); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("archive: %v", err)
		}
		// Stop stays allowed (nothing runs).
		if _, err := sub.Call("stop", nil, p2test.Background); err != nil {
			t.Error(err)
		}
		sub.Release()
		control.Release()
		browser.Release()
	})
}

func TestCodeModeOffWhileTheAgentPluginStays(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	answered := func(text string) delta.JsonValue {
		return f.until(f.Chat.Member("view"), func(v delta.JsonValue) bool { return strings.Contains(marshal(t, v), "Echo: "+text) && idle(v) })
	}
	f.send("with code mode")
	listed := sectionsOf(answered("with code mode"))
	if !strings.Contains(listed["codemode"], agentDescription) {
		t.Error("code mode lists the agent tool with its description")
	}
	if _, has := listed["agent"]; has {
		t.Error("the agent tool has a section of its own")
	}
	// Code mode off (its setting turns the extension off live); the agent plugin stays. The worker's settings observer is
	// asynchronous: turns go until one runs without the listing.
	settings := filepath.Join(f.Fixture.DataDir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"codemode":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	turn := func(prefix string, done func(map[string]string) bool) {
		t.Helper()
		for attempt := 0; attempt < 20; attempt++ {
			text := prefix + " " + itoa(attempt)
			f.send(text)
			if done(sectionsOf(answered(text))) {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("the worker never applied the settings change (%s)", prefix)
	}
	turn("without code mode", func(s map[string]string) bool { _, has := s["codemode"]; return !has })
	// The agent extension stayed installed: with code mode back, it is listed again and scripts can call it.
	if err := os.WriteFile(settings, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	turn("with code mode again", func(s map[string]string) bool { _, has := s["codemode"]; return has })
	f.send(`codemode return await tools.agent({ prompt: 'json {"back":true}', schema: { type: "object" } });`)
	view := f.until(f.Chat.Member("view"), func(v delta.JsonValue) bool { return codemodeResult(v) != nil && idle(v) })
	if codemodeResult(view).Value("isError") != false {
		t.Fatal(agentResultText(codemodeResult(view)))
	}
	if got := scriptOutput(t, agentResultText(codemodeResult(view))); !strings.Contains(got, `"back":true`) {
		t.Errorf("output = %q", got)
	}
}
