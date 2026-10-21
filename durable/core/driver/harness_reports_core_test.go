// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Ports packages/durable/test/harness-reports.test.ts (Pi 482c1381a0) over both engines: what each report to OnReport says
// about its source (ReportInfo). The one expectation that needs a failing Storage commit (:229 session_failed) runs on the
// reference only, in durable/harness (harness_reports_test.go).

type reportOpen struct {
	h       harness.Harness
	reports *infoLog
}

// openReports opens a Harness over the reference or the core with registry and the options configure sets, collecting reports.
func openReports(t *testing.T, core bool, registry harness.Registry, models durable.Models, configure func(*harness.HarnessOptions)) reportOpen {
	t.Helper()
	reports := &infoLog{}
	options := harness.HarnessOptions{Models: models, Registry: registry, OnReport: reports.add}
	if configure != nil {
		configure(&options)
	}
	var h harness.Harness
	var err error
	if core {
		h, err = driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), options)
	} else {
		h, err = harness.OpenHarness(t.Context(), storage.NewMemoryStorage(), options)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return reportOpen{h: h, reports: reports}
}

func findReport(log *infoLog, message string) (reported, bool) {
	for _, item := range log.all() {
		if item.err.Error() == message {
			return item, true
		}
	}
	return reported{}, false
}

func TestReports(t *testing.T) {
	// harness-reports.test.ts:728 "say which task reported a failure or faulted"
	t.Run("say which task reported a failure or faulted", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			faulty := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.faulty", Version: 1,
				Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
					"run": func(_ context.Context, _ loopRecord, runtime loopRuntime) error {
						runtime.Report(errors.New("noted"))
						return errors.New("broken")
					},
				},
				Abort: func(context.Context, loopRecord, loopRuntime) error { return nil },
			})
			registry := harness.CreateRegistry()
			installTasks(t, registry, "test", faulty.Erased())
			opened := openReports(t, core, registry, ai.CreateModels(), nil)
			root := rootOf(t, opened.h)
			id := startOwned(t, root, faulty.Erased())
			opened.h.Resume()
			if outcome := waitFinished2(t, opened.h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeFaulted {
				t.Fatalf("outcome %+v, want faulted", outcome)
			}
			pollUntil(t, "two reports", func() bool { return len(opened.reports.all()) == 2 })
			// The task's own failures name the extension that installed it.
			task := &durable.ReportTask{Id: id, Kind: "test.faulty", ConversationId: root.Id()}
			got := opened.reports.all()
			want := []reported{
				{errors.New("noted"), durable.ReportInfo{Type: durable.ReportTaskError, Task: task, Extension: "test"}},
				{errors.New("broken"), durable.ReportInfo{Type: durable.ReportTaskFaulted, Task: task, Extension: "test", Reason: "threw"}},
			}
			for i := range want {
				if got[i].err.Error() != want[i].err.Error() || !reflect.DeepEqual(got[i].info, want[i].info) {
					t.Fatalf("report %d = %q %+v, want %q %+v", i, got[i].err, got[i].info, want[i].err, want[i].info)
				}
			}
		})
	})

	// harness-reports.test.ts:760 "name the extension whose definition ran, also after another one replaced it"
	t.Run("name the extension whose definition ran, also after another one replaced it", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			gate, started := make(chan struct{}), make(chan struct{})
			var once sync.Once
			definition := func(name string) durable.Task[durable.JsonValue, loopState, durable.JsonValue, any] {
				return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
					Name: "test.replaced", Version: 1,
					Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
					Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
						"run": func(context.Context, loopRecord, loopRuntime) error {
							once.Do(func() { close(started) })
							<-gate
							return errors.New("broken in " + name)
						},
					},
					Abort: func(context.Context, loopRecord, loopRuntime) error { return nil },
				})
			}
			oldTask := definition("old")
			registry := harness.CreateRegistry()
			opened := openReports(t, core, registry, ai.CreateModels(), nil)
			old := &durable.Extension{Name: "old-extension", Tasks: []durable.AnyTask{oldTask.Erased()}}
			if err := registry.Install(old); err != nil {
				t.Fatal(err)
			}
			id := startOwned(t, rootOf(t, opened.h), oldTask.Erased())
			opened.h.Resume()
			<-started
			registry.Uninstall(old)
			if err := registry.Install(&durable.Extension{Name: "new-extension", Tasks: []durable.AnyTask{definition("new").Erased()}}); err != nil {
				t.Fatal(err)
			}
			close(gate)
			waitFinished2(t, opened.h, id)
			var faulted reported
			pollUntil(t, "the fault report", func() bool {
				for _, item := range opened.reports.all() {
					if item.info.Type == durable.ReportTaskFaulted {
						faulted = item
						return true
					}
				}
				return false
			})
			if faulted.err.Error() != "broken in old" || faulted.info.Extension != "old-extension" {
				t.Fatalf("report %q %+v, want broken in old from old-extension", faulted.err, faulted.info)
			}
		})
	})

	// harness-reports.test.ts:798 "name the extension of a failing section or hook, and tell them from a failing environment builder"
	t.Run("name the extension of a failing section or hook, and tell them from a failing environment builder", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1"}}})
			models := ai.CreateModels()
			models.SetProvider(faux.Provider())
			faux.SetResponses([]ai.FauxResponseStep{fauxText("answer")})
			registry := harness.CreateRegistry()
			broken := harness.Section("broken", func(context.Context, durable.PromptInput) (*string, error) { return nil, errors.New("section") })
			if err := registry.Install(&durable.Extension{Name: "sections", Sections: []*durable.PromptSection{broken}}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Install(&durable.Extension{Name: "hooks", Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
				AfterResponse: func(context.Context, ai.AssistantMessage, harness.HookApi) error { return errors.New("hook") },
			})}}); err != nil {
				t.Fatal(err)
			}
			opened := openReports(t, core, registry, models, func(o *harness.HarnessOptions) {
				o.Env = func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) { return nil, errors.New("env") }
			})
			root, err := opened.h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
			if err != nil {
				t.Fatal(err)
			}
			submission, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")})
			if err != nil {
				t.Fatal(err)
			}
			settledOf(t, submission)
			infoOf := func(message string) durable.ReportInfo {
				item, ok := findReport(opened.reports, message)
				if !ok {
					t.Fatalf("no report %q in %v", message, opened.reports.infos())
				}
				return item.info
			}
			generation := func(info durable.ReportInfo) bool {
				return info.Task != nil && info.Task.Kind == "pi.generation" && info.Task.ConversationId == root.Id()
			}
			if info := infoOf("env"); info.Type != durable.ReportHostError || info.Callback != durable.HostCallbackEnv || !generation(info) {
				t.Fatalf("env report %+v, want host_error env of the generation", info)
			}
			if info := infoOf("section"); info.Type != durable.ReportExtensionError || info.Extension != "sections" || !generation(info) {
				t.Fatalf("section report %+v, want extension_error of sections in the generation", info)
			}
			if info := infoOf("hook"); info.Type != durable.ReportExtensionError || info.Extension != "hooks" || !generation(info) {
				t.Fatalf("hook report %+v, want extension_error of hooks in the generation", info)
			}
		})
	})

	// harness-reports.test.ts:846 "tell extension, listener, wake, and timer failures from a failed Session"
	t.Run("tell extension, listener, wake, and timer failures from a failed Session", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			sleeper := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
				Name: "test.sleeper", Version: 1,
				Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
					"run": func(ctx context.Context, _ loopRecord, runtime loopRuntime) error {
						// The host's setTimer throws: reported, and armed with a Go timer instead.
						runtime.SetTimer(func() {}, 10)
						return runtime.SleepUntil(ctx, runtime.Now()+60_000, loopState{Phase: "done"})
					},
					"done": func(ctx context.Context, _ loopRecord, runtime loopRuntime) error {
						return runtime.Commit(ctx, func(durable.Tx, loopRecord) (*loopNext, error) { return loopCompleted(nil), nil })
					},
				},
				Abort: func(context.Context, loopRecord, loopRuntime) error { return nil },
			})
			registry := harness.CreateRegistry()
			installTasks(t, registry, "test", sleeper.Erased())
			opened := openReports(t, core, registry, ai.CreateModels(), func(o *harness.HarnessOptions) {
				o.OnWake = func(harness.Wake) { panic(errors.New("alarm")) }
				o.SetTimer = func(func(), float64, bool) func() { panic(errors.New("no timers")) }
			})
			echo := harness.DefineTool(durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{Name: "echo", Description: "echo", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
				Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					return durable.ToolExecutionResult{}, nil
				},
			})
			if err := registry.Install(&durable.Extension{Name: "tools", Tools: []*durable.ToolRegistration{echo}}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Install(&durable.Extension{Name: "wraps", Wraps: []durable.Wrap{harness.WrapTool(echo, func(tool *durable.ToolRegistration) *durable.ToolRegistration {
				renamed := *tool
				renamed.Name = "renamed"
				return &renamed
			})}}); err != nil {
				t.Fatal(err)
			}
			opened.h.SubscribeCommits(func(context.Context, durable.CommitPublication) { panic(errors.New("listener")) })
			root := rootOf(t, opened.h)
			if _, err := root.Agent(t.Context()); err != nil {
				t.Fatal(err)
			}
			startOwned(t, root, sleeper.Erased())
			opened.h.Resume()
			// The commit listener runs after the commit that publishes it, so it reports on its own schedule: wait for every report.
			pollUntil(t, "the alarm, timer, wrapper and listener reports", func() bool {
				for _, message := range []string{"alarm", "no timers", "listener", "Wrapper renamed echo to renamed"} {
					if _, ok := findReport(opened.reports, message); !ok {
						return false
					}
				}
				return true
			})
			infoOf := func(message string) durable.ReportInfo {
				item, ok := findReport(opened.reports, message)
				if !ok {
					t.Fatalf("no report %q in %v", message, opened.reports.infos())
				}
				return item.info
			}
			wantWrap := durable.ReportInfo{Type: durable.ReportExtensionError, ConversationId: new(root.Id()), Extension: "wraps"}
			if got := infoOf("Wrapper renamed echo to renamed"); !reflect.DeepEqual(got, wantWrap) {
				t.Fatalf("wrapper report %+v, want %+v", got, wantWrap)
			}
			if got := infoOf("listener"); !reflect.DeepEqual(got, durable.ReportInfo{Type: durable.ReportListenerError}) {
				t.Fatalf("listener report %+v, want listener_error", got)
			}
			if got := infoOf("alarm"); !reflect.DeepEqual(got, durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackOnWake}) {
				t.Fatalf("alarm report %+v, want host_error onWake", got)
			}
			if got := infoOf("no timers"); got.Type != durable.ReportHostError || got.Callback != durable.HostCallbackSetTimer {
				t.Fatalf("timer report %+v, want host_error setTimer", got)
			}
		})
	})
}
