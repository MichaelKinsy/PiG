// Ports test/p2/prompt.test.ts and the worker case of test/p2/prompt-templates.test.ts (Pi main c23b9ec1): the app guide,
// global and project AGENTS.md and skills reach the model and edits apply live; a locked session does not get the owner's
// global instructions and skills unless it picked them; `PromptResources` serves participants the templates and skills where
// the conversation works, live, and expands with them (the TUI's completion and the web composer's expander are other
// lanes).

package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	promptcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newPromptSession(t *testing.T, fixture *p2test.Fixture, owner host.Host) string {
	t.Helper()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	return object(created).Value("id").(string)
}

func TestPromptResourcesReachTheModelAndApplyLive(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	writeFile(t, filepath.Join(fixture.DataDir, "AGENTS.md"), "Global rule: be brief.")
	writeFile(t, filepath.Join(fixture.Workspace, "AGENTS.md"), "Project rule: use tabs.")
	writeFile(t, filepath.Join(fixture.DataDir, "skills", "deploy", "SKILL.md"), "---\nname: deploy\ndescription: Deploys the app.\n---\nRun the deploy script.")
	owner := fixture.Browser()
	session := newPromptSession(t, fixture, owner)
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	sent := 0
	send := func(text string) {
		t.Helper()
		sent++
		if _, err := chat.Call("send", delta.JsonObjectOf("text", text, "requestId", "r"+itoa(sent)), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	until := func(predicate func(map[string]string) bool) map[string]string {
		t.Helper()
		view, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool { return predicate(sectionsOf(view)) }, 0)
		if err != nil {
			latest, _ := chat.Member("view").Value()
			keys := []string{}
			for key := range sectionsOf(latest) {
				keys = append(keys, key)
			}
			t.Fatalf("the sections in effect: %q; %.60v", keys, err)
		}
		return sectionsOf(view)
	}

	t.Run("puts the app guide, instructions and skills into the system prompt", func(t *testing.T) {
		send("hello")
		prompt := until(func(sections map[string]string) bool { return sections["agents-md"] != "" })
		if !regexp.MustCompile(`running inside Pi \(p2`).MatchString(prompt["app"]) {
			t.Errorf("app = %q", prompt["app"])
		}
		if !strings.Contains(prompt["app"], fixture.DataDir) {
			t.Errorf("the app guide names the data directory %q: %q", fixture.DataDir, prompt["app"])
		}
		if !regexp.MustCompile(`Global rule: be brief\.[\s\S]*Project rule: use tabs\.`).MatchString(prompt["agents-md"]) {
			t.Errorf("agents-md = %q", prompt["agents-md"])
		}
		if !strings.Contains(prompt["skills"], "<name>deploy</name><description>Deploys the app.</description>") {
			t.Errorf("skills = %q", prompt["skills"])
		}
		if !strings.Contains(prompt["preamble"], "coding assistant") {
			t.Errorf("preamble = %q", prompt["preamble"])
		}
	})
	t.Run("applies edits without restarting the worker", func(t *testing.T) {
		writeFile(t, filepath.Join(fixture.Workspace, "AGENTS.md"), "Project rule: use spaces.")
		// The watch reloads; the next generation renders the new text.
		time.Sleep(500 * time.Millisecond)
		send("again")
		until(func(sections map[string]string) bool { return strings.Contains(sections["agents-md"], "use spaces") })
	})
	t.Run("leaves the owner's global instructions and skills out of a locked session that did not pick them", func(t *testing.T) {
		locks, err := p2test.HoldIn(fixture.Browser(), sessionscontract.SessionLock, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		lock := delta.JsonObjectOf("mcp", []any{}, "projectMcp", false, "instructions", false, "capabilities", []any{})
		if _, err := locks.Call("set", delta.JsonObjectOf("session", session, "lock", lock), p2test.Background); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		send("locked")
		prompt := until(func(sections map[string]string) bool { return !strings.Contains(sections["agents-md"], "Global rule") })
		if !strings.Contains(prompt["agents-md"], "Project rule") {
			t.Errorf("agents-md = %q", prompt["agents-md"])
		}
		if strings.Contains(prompt["skills"], "deploy") {
			t.Errorf("skills = %q", prompt["skills"])
		}
		if _, err := locks.Call("set", delta.JsonObjectOf("session", session, "lock", nil), p2test.Background); err != nil {
			t.Fatal(err)
		}
	})
}

func promptJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestPromptResourcesOnTheWorkerParticipantsOnlyLiveTemplatesExpandWithSkills(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	writeFile(t, filepath.Join(fixture.DataDir, "prompts", "greet.md"), "---\ndescription: Greets\n---\nHello $1!")
	writeFile(t, filepath.Join(fixture.DataDir, "skills", "deploy", "SKILL.md"), "---\nname: deploy\ndescription: Deploys the app.\n---\nRun the deploy script.")
	owner := fixture.Browser()
	session := newPromptSession(t, fixture, owner)
	resources, err := p2test.HoldIn(owner, promptcontract.PromptResources, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	populated := func(source p2test.JSONSource, wanted func([]any) bool) []any {
		t.Helper()
		value, err := p2test.Until(source, func(value delta.JsonValue) bool { return wanted(list(value)) }, 0)
		if err != nil {
			t.Fatal(err)
		}
		return list(value)
	}
	templates := populated(resources.Member("templates"), func(value []any) bool { return len(value) > 0 })
	if got := promptJSON(t, templates); got != `[{"name":"greet","description":"Greets","source":"global"}]` {
		t.Fatalf("templates = %s", got)
	}
	skills := populated(resources.Member("skills"), func(value []any) bool { return len(value) > 0 })
	if got := promptJSON(t, skills); got != `[{"name":"deploy","description":"Deploys the app."}]` {
		t.Fatalf("skills = %s", got)
	}
	expand := func(text string) *delta.JsonObject {
		t.Helper()
		answered, err := resources.Call("expand", delta.JsonObjectOf("text", text), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(answered)
	}
	if got := promptJSON(t, expand("/greet world")); got != `{"text":"Hello world!"}` {
		t.Fatalf("expand template = %s", got)
	}
	skill := expand("/skill:deploy go")
	if object(skill.Value("skill")).Value("name") != "deploy" {
		t.Fatalf("skill = %v", skill)
	}
	if !regexp.MustCompile(`^<skill name="deploy" location="[^"]+SKILL\.md">\n[\s\S]*Run the deploy script\.\n</skill>\n\ngo$`).MatchString(skill.Value("text").(string)) {
		t.Fatalf("skill text = %q", skill.Value("text"))
	}
	if got := promptJSON(t, expand("no command")); got != `{"text":"no command"}` {
		t.Fatalf("expand plain = %s", got)
	}
	// The project's templates appear live.
	writeFile(t, filepath.Join(fixture.Workspace, ".pi", "prompts", "plan.md"), "Plan $@")
	populated(resources.Member("templates"), func(value []any) bool {
		for _, each := range value {
			if object(each).Value("name") == "plan" {
				return true
			}
		}
		return false
	})
	// A view-only guest cannot hold it.
	viewer := guestOf(t, fixture, owner, session, "view", "Viewer")
	denied, err := viewer.Hold(p2test.Background, promptcontract.PromptResources, delta.JsonObjectOf("session", session, "conversation", float64(1)), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, denied), data.CodeForbidden)
}
