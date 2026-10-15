package codingagent

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi awaits model_select without blocking its event loop. Simulate a subprocess
// handler whose tool registration waits for a refresh on the UI owner.
type toolRegisteringModelHandle struct {
	recordingCompactHandle
	mode *InteractiveMode
}

func (h *toolRegisteringModelHandle) refreshFromSubprocess() error {
	result := make(chan error, 1)
	go func() { result <- h.mode.refreshSessionTools(true) }()
	return <-result
}
func (h *toolRegisteringModelHandle) RefreshTools() error { return nil }
func (h *toolRegisteringModelHandle) SetModelOnMain(_ context.Context, model *ai.Model, options ModelMutationOptions, dispatch func(func() error) error) error {
	if err := dispatch(func() error { return h.SetModel(model, options) }); err != nil {
		return err
	}
	return h.refreshFromSubprocess()
}
func (h *toolRegisteringModelHandle) BeginModelCycleOnMain(ctx context.Context, direction string, options ModelMutationOptions) (*ModelCycleResult, func(), error) {
	result, _, err := h.recordingCompactHandle.BeginModelCycleOnMain(ctx, direction, options)
	return result, func() { _ = h.refreshFromSubprocess() }, err
}

func TestInteractiveModelSelectionPumpsToolRegistration(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		name := "selector"
		if cycle {
			name = "cycle"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := modelPickerTestMode(t)
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				m.runCtx = ctx
				m.setModalInputChannel(nil)
				next := &ai.Model{ID: "next", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}
				h := &toolRegisteringModelHandle{mode: m, recordingCompactHandle: recordingCompactHandle{agent: m.agent, cycleResults: []*ModelCycleResult{{Model: next}}}}
				m.opts.SessionHandle = h
				if cycle {
					m.cycleModel(true)
					// The key handler does not await the cycle (interactive-mode.ts:3083-3084); the owner loop finishes it.
					serveOwnerUntil(t, m, ctx, func() bool { return m.lastStatusMessage == "Switched to next" }, func() string {
						return "model cycle blocked its tool-registration callback"
					})
				} else {
					if err := settleSwitchModel(t, m, m.buildSlashContext(ctx), "capture/next"); err != nil {
						t.Fatalf("model selection blocked its tool-registration callback: %v", err)
					}
					if len(h.switched) != 1 {
						t.Fatal("model selection never completed")
					}
				}
				if ctx.Err() != nil {
					t.Fatal("model switch waited until cancellation")
				}
				if m.modalInputCh != nil {
					t.Fatal("model switch retained its modal input route")
				}
			})
		})
	}
}
