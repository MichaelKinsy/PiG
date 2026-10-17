// Ports packages/durable/test/codemode-bench.ts.

package codemode

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// BenchmarkDurableCodeModeOverhead is the overhead of durable code mode. One model-issued call makes N calls of a trivial
// `echo` tool, either sequentially or all at once, in two ways: a code mode script (`tools.echo()`, journaled and
// replayable) and a plain tool calling ExecuteTool the same way (nested calls, no journal). It reports the commits per call
// on SQLite besides the time.
func BenchmarkDurableCodeModeOverhead(b *testing.B) {
	for _, shape := range []string{"sequential", "parallel"} {
		for _, n := range []int{100, 1000} {
			for _, mode := range []string{"plain", "codemode"} {
				b.Run(fmt.Sprintf("%s/n=%d/%s", shape, n, mode), func(b *testing.B) {
					for range b.N {
						b.StopTimer()
						commits := benchRun(b, mode, shape, n)
						b.ReportMetric(float64(commits)/float64(n), "commits/call")
					}
				})
			}
		}
	}
}

func benchRun(b *testing.B, mode, shape string, n int) int64 {
	b.Helper()
	sandbox, err := cm.NewSandbox(cm.SandboxOptions{MemoryLimitBytes: 256 * 1024 * 1024})
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := sandbox.Close(); err != nil {
			b.Error(err)
		}
	}()
	setup := chatSetup()
	echoSchema := map[string]any{"type": "object", "properties": map[string]any{"i": map[string]any{"type": "number"}}, "required": []any{"i"}}
	plainSchema := map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}, "shape": map[string]any{"type": "string"}}, "required": []any{"n", "shape"}}
	echo := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "echo", Description: "echo", Parameters: echoSchema},
		Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input := args.(map[string]any)
			return textResult(fmt.Sprint(input["i"])), nil
		},
	})
	plain := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "plain", Description: "plain", Parameters: plainSchema},
		Replay:     durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input := args.(map[string]any)
			count := int(input["n"].(float64))
			if input["shape"] == "sequential" {
				for i := range count {
					if _, err := api.ExecuteTool(ctx, "echo", map[string]any{"i": float64(i)}); err != nil {
						return durable.ToolExecutionResult{}, err
					}
				}
			} else {
				var wg sync.WaitGroup
				errs := make(chan error, count)
				for i := range count {
					wg.Go(func() {
						_, err := api.ExecuteTool(ctx, "echo", map[string]any{"i": float64(i)})
						errs <- err
					})
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
				}
			}
			return textResult("done"), nil
		},
	})
	if err := setup.registry.Install(&durable.Extension{Name: "bench", Tools: []*durable.ToolRegistration{echo, plain, CreateTool(ToolOptions{Sandbox: sandbox})}}); err != nil {
		b.Fatal(err)
	}
	code := fmt.Sprintf(`for (let i = 0; i < %d; i++) await tools.echo({ i }); return "done";`, n)
	if shape == "parallel" {
		code = fmt.Sprintf(`await Promise.all(Array.from({ length: %d }, (_, i) => tools.echo({ i }))); return "done";`, n)
	}
	call := ai.FauxToolCall("plain", map[string]any{"n": float64(n), "shape": shape}, &ai.FauxToolCallOptions{ID: "c1"})
	if mode == "codemode" {
		call = ai.FauxToolCall(ToolName, map[string]any{"code": code}, &ai.FauxToolCallOptions{ID: "c1"})
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{call}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}}),
	})
	storage, err := openSqliteStorage(filepath.Join(b.TempDir(), "session.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	opened, err := harness.OpenHarness(testContext, storage, harness.HarnessOptions{Models: setup.models, Registry: setup.registry})
	if err != nil {
		b.Fatal(err)
	}
	root, err := opened.Root(testContext, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		b.Fatal(err)
	}
	var commits atomic.Int64
	unsubscribe := opened.SubscribeCommits(func(context.Context, durable.CommitPublication) { commits.Add(1) })
	defer unsubscribe()
	b.StartTimer()
	submission, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := submission.Wait(testContext); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	if err := opened.Close(testContext); err != nil {
		b.Fatal(err)
	}
	return commits.Load()
}
