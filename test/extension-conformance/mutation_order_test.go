package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// Pi has one withFileMutationQueue that the built-in edit and write tools and every extension share, and it registers a call in the order the calls were made
// (core/tools/file-mutation-queue.ts: registrationQueue). Promise.all(calls.map(...)) runs each call's synchronous prefix in call order
// (agent-loop.ts:619-647), so an extension tool that takes the queue in its first statement registers before the write call that follows it in the batch, and
// after the one that precedes it. PiG's extension runs in another process, so the host's queue sees the extension's registration a hop after it started the call.
// Each case is a batch of a mutation_order_probe call (the extension appends its tag to the file inside the queue) and a built-in write call (it replaces the
// file); the file shows which registered first: the write replaced the tag when the extension was first, and the tag follows the written text when the write was.
//
// The Node realizations add a tool that awaits the event loop before it takes the queue, where Pi registers the write first: the runtime reports the await, the host
// registers the write, and the runtime holds the tool's registration until it did. A native handler cannot tell such an await from work of its own, so it has no row.
func TestFileMutationOrderAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	type batch struct {
		name string
		// probeFirst puts the extension call before the write call.
		probeFirst bool
		late       string
		applies    func(realization string) bool
		want       string
		upstream   string
	}
	batches := []batch{
		{name: "the extension tool takes the queue first and the write follows", probeFirst: true, applies: everyRealization, want: "WRITE\n",
			upstream: "agent-loop.ts:646-648, file-mutation-queue.ts:32-45: the extension's withFileMutationQueue call, made in its synchronous prefix, registers before the write's"},
		{name: "the write precedes the extension tool", probeFirst: false, applies: everyRealization, want: "WRITE\nEXT\n",
			upstream: "agent-loop.ts:646-648: the write's synchronous prefix runs before the extension tool's"},
		{name: "the extension tool awaits the event loop before it takes the queue", probeFirst: true, late: "loop", applies: firstStatementOrder, want: "WRITE\nEXT\n",
			upstream: "agent-loop.ts:646-648: the extension tool's prefix ends at its first await, so the write registers first"},
	}
	for _, tc := range orderingCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			dir := t.TempDir()
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Runner: h.runner, Tools: tools, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range session.Events() {
				}
			}()
			t.Cleanup(func() { _ = session.Close(); <-done })
			for _, b := range batches {
				if !b.applies(tc.name) {
					continue
				}
				for round := range 6 {
					file := filepath.Join(dir, fmt.Sprintf("%s-%d.txt", tc.name, round))
					if err := os.WriteFile(file, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					probe := ai.FauxToolCall("mutation_order_probe", map[string]any{"file": file, "tag": "EXT", "late": b.late}, &ai.FauxToolCallOptions{ID: fmt.Sprintf("probe-%d", round)})
					write := ai.FauxToolCall("write", map[string]any{"path": file, "content": "WRITE\n"}, &ai.FauxToolCallOptions{ID: fmt.Sprintf("write-%d", round)})
					calls := []ai.FauxContentBlock{write, probe}
					if b.probeFirst {
						calls = []ai.FauxContentBlock{probe, write}
					}
					provider.SetResponses([]ai.FauxResponseStep{
						ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"}),
						ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
					})
					if _, err := session.Send(t.Context(), "go"); err != nil {
						t.Fatal(err)
					}
					got, err := os.ReadFile(file)
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != b.want {
						t.Errorf("%s, round %d: the file holds %q, want %q\n  Pi: %s", b.name, round, got, b.want, b.upstream)
					}
				}
			}
		})
	}
}
