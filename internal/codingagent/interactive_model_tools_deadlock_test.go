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
func (h *toolRegisteringModelHandle) SetModel(model *ai.Model, opts ...ModelMutationOptions) error {
	if err := h.refreshFromSubprocess(); err != nil {
		return err
	}
	return h.recordingCompactHandle.SetModel(model, opts...)
}
func (h *toolRegisteringModelHandle) CycleModel(direction string, opts ...ModelMutationOptions) (*ModelCycleResult, error) {
	if err := h.refreshFromSubprocess(); err != nil {
		return nil, err
	}
	return h.recordingCompactHandle.CycleModel(direction, opts...)
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
				next := &ai.Model{ID: "next", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}
				h := &toolRegisteringModelHandle{mode: m, recordingCompactHandle: recordingCompactHandle{agent: m.agent, cycleResults: []*ModelCycleResult{{Model: next}}}}
				m.opts.SessionHandle = h
				if cycle {
					m.cycleModel(true)
					if len(h.cycled) != 1 {
						t.Fatal("model cycle blocked its tool-registration callback")
					}
				} else {
					if err := m.buildSlashContext(ctx).SwitchModel("capture/next"); err != nil {
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
