package evalsuites

// Ports packages/evals/evals/tui.docs.eval.ts.
//
// Pi's oracle builds an in-process InteractiveMode over a recording terminal. PiG's interactive mode is the pig
// binary, so the oracle renders it in a tmux pane over a session file whose last assistant message carries the
// fixture's context usage, with the agent's extension loaded from the run's agent directory and the model's context
// window set to 272k through models.json modelOverrides. The built-in status text for comparison comes from the
// same render with extensions disabled (Pi reads it from the first render of the extended session).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

const contextWindow = 272_000

var builtInContextPattern = regexp.MustCompile(`\d+(?:\.\d+)?%/272k(?: \(auto\))?`)

type contextFixture struct {
	percent     float64
	expectedBar string
}

var contextFixtures = []contextFixture{
	{42.2, "████░░░░░░ 42%"},
	{65, "███████░░░ 65%"},
	{120, "██████████ 100%"},
}

type contextUsage struct {
	Tokens        *float64 `json:"tokens"`
	ContextWindow float64  `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

type footerObservation struct {
	Percent        float64       `json:"percent"`
	RenderedOutput string        `json:"renderedOutput"`
	ContextUsage   *contextUsage `json:"contextUsage"`
}

// TUIFooterOutput is the oracle's observation of the footer.
type TUIFooterOutput struct {
	BuiltInStatusLine *string             `json:"builtInStatusLine"`
	Observations      []footerObservation `json:"observations"`
	ExtensionErrors   []string            `json:"extensionErrors"`
}

func nonEmptyLines(output string) []string {
	var lines []string
	for line := range strings.SplitSeq(strings.NewReplacer("\r", "\n").Replace(output), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func normalizeSpaces(text string) string { return strings.Join(strings.Fields(text), " ") }

// sessionFixture writes a session whose last assistant message uses percent of the context window.
func sessionFixture(directory, workspace string, selection evals.PiCodingAgentModelSelection, percent float64) (string, error) {
	tokens := math.Round(contextWindow * percent / 100)
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	cost := map[string]float64{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}
	entries := []any{
		map[string]any{"type": "session", "version": 3, "id": "eval-context-footer", "timestamp": timestamp, "cwd": workspace},
		map[string]any{"type": "message", "id": "u1", "parentId": nil, "timestamp": timestamp, "message": map[string]any{
			"role": "user", "content": "Context usage fixture", "timestamp": time.Now().UnixMilli()}},
		map[string]any{"type": "message", "id": "a1", "parentId": "u1", "timestamp": timestamp, "message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Context usage fixture"}}, "api": "openai-completions",
			"provider": selection.Provider, "model": selection.ID, "stopReason": "stop", "timestamp": time.Now().UnixMilli(),
			"usage": map[string]any{"input": tokens, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": tokens, "cost": cost}}},
	}
	var lines []string
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return "", err
		}
		lines = append(lines, string(encoded))
	}
	path := filepath.Join(directory, fmt.Sprintf("context-%v.jsonl", percent))
	return path, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// overrideContextWindow copies agentDir and sets selection's context window to 272k through models.json.
func overrideContextWindow(agentDir, scratch string, selection evals.PiCodingAgentModelSelection) (string, error) {
	copied := filepath.Join(scratch, "agent")
	if err := os.CopyFS(copied, os.DirFS(agentDir)); err != nil {
		return "", err
	}
	config := map[string]any{}
	modelsPath := filepath.Join(copied, "models.json")
	if data, err := os.ReadFile(modelsPath); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return "", err
		}
	}
	providers, _ := config["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	provider, _ := providers[selection.Provider].(map[string]any)
	if provider == nil {
		provider = map[string]any{}
	}
	overrides, _ := provider["modelOverrides"].(map[string]any)
	if overrides == nil {
		overrides = map[string]any{}
	}
	override, _ := overrides[selection.ID].(map[string]any)
	if override == nil {
		override = map[string]any{}
	}
	override["contextWindow"] = contextWindow
	overrides[selection.ID], provider["modelOverrides"], providers[selection.Provider], config["providers"] = override, overrides, provider, providers
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return copied, os.WriteFile(modelsPath, encoded, 0o600)
}

// renderFooter runs pig interactively in a tmux pane until the footer shows the context status, and returns the pane.
func renderFooter(ctx context.Context, run evals.AgentRun, agentDir, sessionPath string, extensions bool, ready *regexp.Regexp) (string, error) {
	name := fmt.Sprintf("parity-evals-footer-%d", rand.Uint32())
	home := filepath.Join(filepath.Dir(agentDir), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	args := []string{"new-session", "-d", "-s", name, "-x", "100", "-y", "30", "-c", run.Workspace,
		"-e", "HOME=" + home, "-e", "USERPROFILE=" + home, "-e", "PI_OFFLINE=1", "-e", "TERM=xterm-256color",
		"-e", evals.AgentDirEnvironment() + "=" + agentDir,
		run.PigPath, "--session", sessionPath, "--model", run.Model.Provider + "/" + run.Model.ID, "--offline"}
	if !extensions {
		args = append(args, "--no-extensions")
	}
	if out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("tmux new-session: %w: %s", err, out)
	}
	defer func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() }()
	var pane string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		out, err := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", name).Output()
		if err != nil {
			return pane, fmt.Errorf("pig exited before rendering the footer: %w", err)
		}
		if pane = string(out); ready.MatchString(pane) {
			// Let the footer settle: a later render may replace the status.
			time.Sleep(300 * time.Millisecond)
			out, err = exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-t", name).Output()
			return string(out), err
		}
	}
	return pane, errors.New("the footer did not show the context status")
}

// contextUsageOf reads the Session's context usage from pig's RPC get_session_stats.
func contextUsageOf(ctx context.Context, run evals.AgentRun, agentDir, sessionPath string) (*contextUsage, error) {
	stats, err := evals.RPCSessionStats(ctx, run.PigPath, run.Workspace, []string{"--session", sessionPath, "--model", run.Model.Provider + "/" + run.Model.ID, "--no-extensions"},
		append(os.Environ(), evals.AgentDirEnvironment()+"="+agentDir))
	if err != nil {
		return nil, err
	}
	var decoded struct {
		ContextUsage *contextUsage `json:"contextUsage"`
	}
	return decoded.ContextUsage, json.Unmarshal(stats, &decoded)
}

func inspectContextFooter(ctx context.Context, run evals.AgentRun) (any, error) {
	scratch, err := os.MkdirTemp("", "pi-eval-footer-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	agentDir, err := overrideContextWindow(run.AgentDir, scratch, run.Model)
	if err != nil {
		return nil, err
	}
	output := TUIFooterOutput{Observations: []footerObservation{}, ExtensionErrors: []string{}}
	anyContext := regexp.MustCompile(`\d+%/272k|█|░`)
	for index, fixture := range contextFixtures {
		sessionPath, err := sessionFixture(scratch, run.Workspace, run.Model, fixture.percent)
		if err != nil {
			return nil, err
		}
		if index == 0 {
			baseline, err := renderFooter(ctx, run, agentDir, sessionPath, false, builtInContextPattern)
			if err != nil {
				return nil, err
			}
			for _, line := range nonEmptyLines(baseline) {
				if builtInContextPattern.MatchString(line) {
					output.BuiltInStatusLine = &line
					break
				}
			}
		}
		pane, err := renderFooter(ctx, run, agentDir, sessionPath, true, anyContext)
		if err != nil {
			return nil, err
		}
		usage, err := contextUsageOf(ctx, run, agentDir, sessionPath)
		if err != nil {
			return nil, err
		}
		output.Observations = append(output.Observations, footerObservation{Percent: fixture.percent, RenderedOutput: pane, ContextUsage: usage})
	}
	output.ExtensionErrors = append(output.ExtensionErrors, extensionErrors(run.ExtensionErrors)...)
	return output, nil
}

func levenshteinScore(expected, actual string) float64 {
	left, right := []rune(expected), []rune(actual)
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	previous := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(left); i++ {
		current := make([]int, len(right)+1)
		current[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return 1 - float64(previous[len(right)])/float64(max(len(left), len(right)))
}

type contextFooterJudge struct{}

func (contextFooterJudge) Name() string { return "ContextFooterJudge" }

func (contextFooterJudge) Assess(output any, _ []evals.ToolCall) (evals.JudgeScore, error) {
	encoded, err := json.Marshal(output)
	if err != nil {
		return evals.JudgeScore{}, err
	}
	var footer TUIFooterOutput
	if err := json.Unmarshal(encoded, &footer); err != nil {
		return evals.JudgeScore{}, err
	}
	valid := len(footer.ExtensionErrors) == 0
	var similarities []float64
	const contextMarker = "{context}"
	reference := ""
	if footer.BuiltInStatusLine != nil {
		reference = builtInContextPattern.ReplaceAllString(normalizeSpaces(*footer.BuiltInStatusLine), contextMarker)
	}
	if !strings.Contains(reference, contextMarker) {
		valid = false
	}
	for index, fixture := range contextFixtures {
		if index >= len(footer.Observations) || footer.Observations[index].Percent != fixture.percent {
			valid = false
			continue
		}
		observation := footer.Observations[index]
		if observation.ContextUsage == nil || observation.ContextUsage.ContextWindow != contextWindow || observation.ContextUsage.Percent == nil ||
			math.Round(*observation.ContextUsage.Percent*10)/10 != fixture.percent {
			valid = false
		}
		var statusLine string
		for _, line := range nonEmptyLines(observation.RenderedOutput) {
			if strings.Contains(line, fixture.expectedBar) || builtInContextPattern.MatchString(line) {
				statusLine = line
				break
			}
		}
		if statusLine == "" {
			valid = false
			continue
		}
		normalized := normalizeSpaces(statusLine)
		if !strings.Contains(normalized, fixture.expectedBar) || builtInContextPattern.MatchString(normalized) {
			valid = false
			continue
		}
		for _, other := range contextFixtures {
			if other.expectedBar != fixture.expectedBar && strings.Contains(normalized, other.expectedBar) {
				valid = false
			}
		}
		if reference != "" {
			similarities = append(similarities, levenshteinScore(reference, strings.Replace(normalized, fixture.expectedBar, contextMarker, 1)))
		}
	}
	retained := 0.0
	if len(similarities) == len(contextFixtures) {
		retained = slicesMin(similarities)
	}
	rationale, score := "Context progress behavior was incorrect.", 0.0
	if valid {
		rationale, score = fmt.Sprintf("Footer retained %d%% of the built-in status text.", int(math.Round(retained*100))), 1
	}
	return evals.JudgeScore{Score: score, Metadata: map[string]any{"rationale": rationale, "retainedPercent": math.Round(retained * 100)}}, nil
}

func slicesMin(values []float64) float64 {
	result := values[0]
	for _, value := range values[1:] {
		result = min(result, value)
	}
	return result
}

// TUI asks the agent to replace the footer's context text with a progress bar and renders the result.
func TUI() evals.Suite {
	return evals.Suite{
		Name:             "Customize the interactive context footer",
		File:             "evals/tui.docs.eval.go",
		Harness:          documentationHarness(evals.PiCodingAgentHarnessOptions{Output: inspectContextFooter}),
		Judges:           []evals.Judge{contextFooterJudge{}},
		NoJudgeThreshold: true,
		Cases: []evals.Case{singleCase("replaces numeric context usage with a progress bar", promptThenReload(
			"Change the PiG footer replacing the built-in context info, e.g. `42.2%/272k (auto)`, with a ten-cell progress bar to make it show ████░░░░░░ 42% instead. Clamp percentages to the 0-100 range. Just replace the context. Otherwise leave everything else the same."))},
	}
}
