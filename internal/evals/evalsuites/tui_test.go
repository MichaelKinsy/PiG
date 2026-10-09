package evalsuites

// Tests for the footer oracle and judge of tui.docs.eval.ts. The oracle runs the real pig binary in tmux over fixture
// sessions; the judge cases pin the scoring of tui.docs.eval.ts:174-232.

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

var (
	pigOnce   sync.Once
	pigBinary string
	pigErr    error
)

func builtPig(t *testing.T) string {
	t.Helper()
	pigOnce.Do(func() {
		directory, err := os.MkdirTemp("", "pig-evalsuites-")
		if err != nil {
			pigErr = err
			return
		}
		pigBinary = filepath.Join(directory, "pig")
		if out, err := exec.Command("go", "build", "-o", pigBinary, "../../../cmd/pig").CombinedOutput(); err != nil {
			pigErr = err
			t.Logf("%s", out)
		}
	})
	if pigErr != nil {
		t.Fatal(pigErr)
	}
	return pigBinary
}

func footerOutput(lines ...string) TUIFooterOutput {
	output := TUIFooterOutput{ExtensionErrors: []string{}}
	builtIn := "~/project (main)    42.2%/272k (auto)    fixture-chat"
	output.BuiltInStatusLine = &builtIn
	for index, fixture := range contextFixtures {
		percent := fixture.percent
		rounded := percent
		tokens := 272000 * percent / 100
		output.Observations = append(output.Observations, footerObservation{
			Percent: percent, RenderedOutput: "header\n" + lines[index] + "\n",
			ContextUsage: &contextUsage{Tokens: &tokens, ContextWindow: contextWindow, Percent: &rounded},
		})
	}
	return output
}

func TestContextFooterJudge(t *testing.T) {
	good := footerOutput("~/project (main)    ████░░░░░░ 42%    fixture-chat", "~/project (main)    ███████░░░ 65%    fixture-chat", "~/project (main)    ██████████ 100%    fixture-chat")
	cases := []struct {
		name   string
		output TUIFooterOutput
		score  float64
	}{
		{"progress bars keep the status text", good, 1},
		{"the built-in numeric text remains", footerOutput("~/project (main)    42.2%/272k (auto)    fixture-chat", "~/project (main)    ███████░░░ 65%    fixture-chat", "~/project (main)    ██████████ 100%    fixture-chat"), 0},
		{"a bar from another fixture is wrong", footerOutput("~/project (main)    ████░░░░░░ 42% ███████░░░ 65%    fixture-chat", "~/project (main)    ███████░░░ 65%    fixture-chat", "~/project (main)    ██████████ 100%    fixture-chat"), 0},
		{"an extension error is invalid", func() TUIFooterOutput { o := good; o.ExtensionErrors = []string{"boom"}; return o }(), 0},
		{"no built-in line cannot be compared", func() TUIFooterOutput { o := good; o.BuiltInStatusLine = nil; return o }(), 0},
		{"missing observations are invalid", func() TUIFooterOutput { o := good; o.Observations = o.Observations[:2]; return o }(), 0},
	}
	for _, c := range cases {
		score, err := contextFooterJudge{}.Assess(c.output, nil)
		if err != nil || score.Score != c.score {
			t.Errorf("%s: score = %+v, %v; want %v", c.name, score, err, c.score)
		}
	}
	score, _ := contextFooterJudge{}.Assess(good, nil)
	if rationale, _ := score.Metadata["rationale"].(string); !strings.HasPrefix(rationale, "Footer retained ") || !strings.HasSuffix(rationale, "% of the built-in status text.") {
		t.Errorf("rationale = %q", rationale)
	}
}

func TestLevenshteinScore(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{{"", "", 1}, {"abc", "abc", 1}, {"abc", "", 0}, {"kitten", "sitting", 1 - 3.0/7}} {
		if got := levenshteinScore(c.a, c.b); got != c.want {
			t.Errorf("levenshteinScore(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestInspectContextFooterRendersTheBuiltInFooter runs the oracle over a Session with no footer extension: the pane
// must show the built-in context text for each fixture, and the Session must report each fixture's context usage.
func TestInspectContextFooterRendersTheBuiltInFooter(t *testing.T) {
	pig := builtPig(t)
	root := t.TempDir()
	agentDir, workspace := filepath.Join(root, "agent"), filepath.Join(root, "workspace")
	for _, directory := range []string{agentDir, workspace} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
		"baseUrl": "http://127.0.0.1:9/v1", "api": "openai-completions", "apiKey": "k",
		"models": []any{map[string]any{"id": "fixture-chat", "name": "Fixture Chat", "input": []string{"text"}, "contextWindow": 8192, "maxTokens": 1024}},
	}}})
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(evals.AgentDirEnvironment(), agentDir)
	got, err := inspectContextFooter(t.Context(), evals.AgentRun{
		AgentDir: agentDir, Workspace: workspace, PigPath: pig, Model: evals.PiCodingAgentModelSelection{Provider: "fixture", ID: "fixture-chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := got.(TUIFooterOutput)
	if output.BuiltInStatusLine == nil || !strings.Contains(*output.BuiltInStatusLine, "42.2%/272k") {
		t.Fatalf("built-in status line = %v", output.BuiltInStatusLine)
	}
	for index, want := range []string{"42.2%/272k", "65.0%/272k", "120.0%/272k"} {
		observation := output.Observations[index]
		if !strings.Contains(observation.RenderedOutput, want) {
			t.Errorf("fixture %v pane lacks %q:\n%s", observation.Percent, want, observation.RenderedOutput)
		}
		if observation.ContextUsage == nil || observation.ContextUsage.ContextWindow != contextWindow || observation.ContextUsage.Percent == nil || math.Round(*observation.ContextUsage.Percent*10)/10 != contextFixtures[index].percent {
			t.Errorf("fixture %v context usage = %+v", observation.Percent, observation.ContextUsage)
		}
	}
	score, _ := contextFooterJudge{}.Assess(output, nil)
	if score.Score != 0 {
		t.Errorf("an unchanged footer must not score: %+v", score)
	}
}
