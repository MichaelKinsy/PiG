package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// D654: a print run whose retries a fused member decides retries per the decider and, with Silent, writes no retry line of PiG's own.
func TestPrintModeRetriesPerTheInstalledDeciderAndWritesNoRetryLine(t *testing.T) {
	t.Cleanup(func() { codingagent.SetDefaultRetryDecider(nil, false) })
	var seen []string
	codingagent.SetDefaultRetryDecider(func(attempt, maxAttempts int, response ai.AssistantMessage) ai.RetryDecision {
		seen = append(seen, fmt.Sprintf("%d/%d %s", attempt, maxAttempts, response.ErrorMessage))
		return ai.RetryDecision{Retry: attempt == 1, DelayMs: 1}
	}, true)
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(
		ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded, try later"},
		fauxTextResponse("recovered"),
	))
	result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{Mode: "text", InitialMessage: "hello"})
	if result.err != nil || result.stdout != "recovered\n" || result.stderr != "" {
		t.Fatalf("err %v, stdout %q, stderr %q; want the retried answer and nothing on stderr", result.err, result.stdout, result.stderr)
	}
	if calls := provider.CallCount(); calls != 2 {
		t.Fatalf("model calls = %d, want 2 (the decider retried once)", calls)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "1/3 overloaded") {
		t.Fatalf("decider saw %q, want one failure: attempt 1 of the 3 retries", seen)
	}
}

// D654: a decider that refuses ends the print run with the failure, as PiG's own rule does for an error it does not retry.
func TestPrintModeStopsWhenTheDeciderRefuses(t *testing.T) {
	t.Cleanup(func() { codingagent.SetDefaultRetryDecider(nil, false) })
	codingagent.SetDefaultRetryDecider(func(int, int, ai.AssistantMessage) ai.RetryDecision { return ai.RetryDecision{} }, true)
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(
		ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded, try later"},
		fauxTextResponse("unreachable"),
	))
	result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{Mode: "text", InitialMessage: "hello"})
	if calls := provider.CallCount(); calls != 1 {
		t.Fatalf("model calls = %d, want 1 (the decider refused the retry PiG's own rule would make); err %v stdout %q", calls, result.err, result.stdout)
	}
	if strings.Contains(result.stdout, "unreachable") {
		t.Fatalf("stdout %q carries the second response", result.stdout)
	}
}

// D654: the idle time-out a hook names replaces the default the HTTP dispatcher is configured with when settings.json names none; without one the default
// stays 300000 ms, and settings.json still wins.
func TestHTTPIdleTimeoutOverrideReachesTheDispatcher(t *testing.T) {
	t.Cleanup(func() {
		codingagent.SetDefaultHTTPIdleTimeoutMs(nil)
		_ = ai.ConfigureHTTPDispatcher(ai.DefaultHTTPIdleTimeoutMs)
	})
	configure := func(settings codingagent.Settings) int {
		t.Helper()
		if err := configureHTTPDispatcherFromSettings(codingagent.NewInMemorySettingsManager(settings)); err != nil {
			t.Fatal(err)
		}
		return ai.ConfiguredHTTPIdleTimeoutMs()
	}
	if got := configure(codingagent.Settings{}); got != 300_000 {
		t.Fatalf("default idle timeout = %d, want 300000", got)
	}
	override := 12_000
	codingagent.SetDefaultHTTPIdleTimeoutMs(&override)
	if got := configure(codingagent.Settings{}); got != 12_000 {
		t.Fatalf("overridden idle timeout = %d, want 12000", got)
	}
	named := 45_000
	if got := configure(codingagent.Settings{HTTPIdleTimeoutMs: &named}); got != 45_000 {
		t.Fatalf("settings.json idle timeout = %d, want 45000 over the override", got)
	}
}

// hookChildEnv makes the test binary run Main(--version) with a fused hook registered, and print what the hook's policy installed in PiG.
const hookChildEnv = "PIG_TEST_RETRY_HOOK_CHILD"

// D654: main.go applies a hook's Retry policy (decider and Silent) and HTTPIdleTimeoutMs before any command: the decider reaches the session with the
// failure's attempt, budget, error text, status, headers and stream state, and a hook that names neither leaves PiG's own rules.
func TestMainAppliesTheHooksRetryPolicyAndIdleTimeout(t *testing.T) {
	if mode := os.Getenv(hookChildEnv); mode != "" {
		idle := 9_000
		if mode == "set" {
			clihook.Register(func(env clihook.Env) clihook.Result {
				return clihook.Result{Args: env.Args, HTTPIdleTimeoutMs: &idle, Retry: &clihook.RetryPolicy{MaxRetries: 4, BaseDelayMs: 7, MaxAgentDelayMs: 70, Silent: true,
					Decide: func(failure clihook.RetryFailure) clihook.RetryDecision {
						encoded, _ := json.Marshal(failure)
						fmt.Printf("\nhook-failure %s\n", encoded)
						return clihook.RetryDecision{Retry: true, DelayMs: 42}
					}}}
			})
		} else {
			clihook.Register(func(env clihook.Env) clihook.Result { return clihook.Result{Args: env.Args} })
		}
		stopStartupExtensions = func() {
			decision := ai.RetryDecision{}
			if decider := codingagent.DefaultRetryDecider(); decider != nil {
				decision = decider(2, 4, ai.AssistantMessage{ErrorMessage: "overloaded", FailureResponse: &ai.FailureResponse{Status: 429, Headers: map[string]string{"retry-after": "3"}, StreamStarted: true}})
			}
			settingsIdle, _ := codingagent.NewInMemorySettingsManager(codingagent.Settings{}).GetHttpIdleTimeoutMs()
			fmt.Printf("\nhook-applied idle=%d settingsIdle=%d silent=%v decider=%v decision=%+v retry=%+v\n", codingagent.DefaultHTTPIdleTimeoutMs(), settingsIdle, codingagent.DefaultRetrySilent(), codingagent.DefaultRetryDecider() != nil, decision, codingagent.DefaultRetryConfig())
		}
		Main([]string{"--version"}, nil)
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	run := func(mode string) string {
		t.Helper()
		home := t.TempDir()
		command := exec.Command(os.Args[0], "-test.run=^TestMainAppliesTheHooksRetryPolicyAndIdleTimeout$")
		command.Env = append(os.Environ(), hookChildEnv+"="+mode, "HOME="+home, "USERPROFILE="+home, "PIG_OFFLINE=1")
		out, err := command.Output()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		return string(out)
	}
	set := run("set")
	for _, want := range []string{
		`hook-failure {"Attempt":2,"MaxRetries":4,"ErrorMessage":"overloaded","Status":429,"Headers":{"retry-after":"3"},"StreamStarted":true}`,
		"hook-applied idle=9000 settingsIdle=9000 silent=true decider=true decision={Retry:true DelayMs:42}",
		"MaxRetries:4 BaseDelayMs:7 MaxAgentDelayMs:70",
	} {
		if !strings.Contains(set, want) {
			t.Fatalf("child output lacks %q:\n%s", want, set)
		}
	}
	plain := run("plain")
	if !strings.Contains(plain, "hook-applied idle=300000 settingsIdle=300000 silent=false decider=false decision={Retry:false DelayMs:0} retry={Enabled:true MaxRetries:3 BaseDelayMs:2000 MaxAgentDelayMs:60000}") {
		t.Fatalf("a hook that names no policy changed PiG's defaults:\n%s", plain)
	}
}
