//go:build (linux || darwin) && !race

package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Issue #211: peak RSS of a fresh process running a Codemode script through Execute, the entry the codemode tool calls,
// with extension tools exposed to the script. Each scenario runs in a child (this test binary re-executed) so that
// ru_maxrss belongs to that scenario alone. The worker limit allows two VMs per process, so two is the concurrent case.

// The bounds are for the shipped binary: a -race build raises the no-op scenario's peak to 143 MiB against the 128 MiB bound, so the nightly -race run excludes this file.
const rssChildEnv = "PIG_CODEMODE_RSS_SCENARIO"

func rssTools(n int) []extension.AgentTool {
	schema := `{"type":"object","properties":{"state":{"type":"object","description":"the decision state"},"note":{"type":"string"}},"required":["state"]}`
	tools := make([]extension.AgentTool, n)
	for i := range tools {
		tools[i] = extension.AgentTool{Name: fmt.Sprintf("ext_tool_%d", i), Label: "tool", Description: strings.Repeat("Runs one decision step. ", 12), Parameters: json.RawMessage(schema)}
	}
	return tools
}

func rssState() string {
	var b strings.Builder
	b.WriteString("[")
	for i := range 96 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":%d,"text":"%s"}`, i, strings.Repeat("x", 170))
	}
	b.WriteString("]")
	return b.String()
}

func runRSSScenario(name string) error {
	if name == "process-only" {
		return nil
	}
	var tools []extension.AgentTool
	if name != "noop-no-tools" {
		tools = rssTools(40)
	}
	base := extension.NewContext(os.TempDir(), nil, func() error { return nil }, extension.ContextActions{})
	echo := func(ctx context.Context, _, _ string, args json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
		extension.CallInitiated(ctx)
		return extension.AgentToolCallOutcome{Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: string(args)}}}}, nil
	}
	tc := extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{ExecuteTool: echo, GetCallableTools: func() []extension.AgentTool { return tools }})
	ctx := extension.WithToolContext(context.Background(), tc)
	code := "return 1 + 1"
	runs := 1
	if name == "state-call" || name == "concurrent-2" {
		code = "const state = " + rssState() + "; const r = await tools.ext_tool_0({state: {records: state}}); return r.length;"
	}
	if name == "concurrent-2" {
		runs = 2
	}
	params, _ := json.Marshal(map[string]string{"code": code})
	var wg sync.WaitGroup
	errs := make(chan error, runs)
	for range runs {
		wg.Go(func() {
			res, err := Execute(ctx, "call", params, nil, Options{CacheDir: os.Getenv("RSS_CACHE")})
			if err == nil && strings.HasPrefix(resultText(res), "Script failed") {
				err = fmt.Errorf("script failed: %s", resultText(res))
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func TestRSSScenarioChild(t *testing.T) {
	name := os.Getenv(rssChildEnv)
	if name == "" {
		t.Skip("child of TestPeakRSSOfCodemodeScenarios")
	}
	if err := runRSSScenario(name); err != nil {
		t.Fatal(err)
	}
}

func peakRSS(t *testing.T, scenario string) uint64 {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRSSScenarioChild$")
	cmd.Env = append(os.Environ(), rssChildEnv+"="+scenario)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", scenario, err, out)
	}
	return peakRSSOf(cmd.ProcessState.SysUsage().(*syscall.Rusage))
}

// TestPeakRSSOfCodemodeScenarios bounds the peak RSS of fresh processes, issue #211. Reported before the fix: 391 MiB
// for a no-op with extension tools and 393 MiB with a 96-record state.
func TestPeakRSSOfCodemodeScenarios(t *testing.T) {
	bounds := map[string]uint64{"process-only": rssBoundNoop, "noop-no-tools": rssBoundNoop, "noop-tools": rssBoundNoop, "state-call": rssBoundState, "concurrent-2": rssBoundConcurrent}
	for _, scenario := range []string{"process-only", "noop-no-tools", "noop-tools", "state-call", "concurrent-2"} {
		rss := peakRSS(t, scenario)
		t.Logf("PEAKRSS %s %.1f MiB", scenario, float64(rss)/(1<<20))
		if rss > bounds[scenario] {
			t.Errorf("%s: peak RSS %d MiB, want <= %d MiB", scenario, rss>>20, bounds[scenario]>>20)
		}
	}
}
