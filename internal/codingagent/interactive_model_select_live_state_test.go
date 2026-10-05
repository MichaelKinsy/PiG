package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// liveModelSessionHandle applies a model change as the Session does: it sets the agent's model and thinking level, then awaits the model_select notification before returning (agent-session.ts setModel/cycleModel, _emitModelSelect).
type liveModelSessionHandle struct {
	*recordingCompactHandle
	notify func()
}

func (h *liveModelSessionHandle) apply(model *ai.Model) {
	h.agent.SetModel(model)
	h.agent.SetThinkingLevel(ai.ClampThinkingLevel(model, h.agent.ThinkingLevel()))
	h.notify()
}

func (h *liveModelSessionHandle) CycleToModel(model *ai.Model, options ...ModelMutationOptions) error {
	_ = h.recordingCompactHandle.CycleToModel(model, options...)
	h.apply(model)
	return nil
}

func (h *liveModelSessionHandle) SetModel(model *ai.Model, options ...ModelMutationOptions) error {
	_ = h.recordingCompactHandle.SetModel(model, options...)
	h.apply(model)
	return nil
}

// Issue #128: Pi's extension context reads the Session's model live (runner.ts createContext `get model()` returns getModel(), bound to the Session's agent.state.model in agent-session.ts), and the Session sets the model before it emits model_select. Interactive mode bound the in-process ctx.model and the subprocess getModelInfo state to its own copy of the model, which it updated only after the Session returned, so every model_select and thinking_level_select handler observed the previous model.
func TestInteractiveModelSelectHandlersObserveTheSelectedModel(t *testing.T) {
	m := modelPickerTestMode(t)
	t.Setenv("OPENAI_API_KEY", "fake-openai")
	m.uiTaskCh = make(chan func(), 8)
	m.editor = tui.NewEditor()
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	m.newRunner = modelSelectCountingRunner(t, new([]any))
	m.wireInprocContextActions()
	detach := m.wireSubprocessHostCallbacks()
	defer detach()

	var inproc, subprocess []string
	handle := &liveModelSessionHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}}
	handle.notify = func() {
		model, err := m.newRunner.CreateCommandContext().Model()
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := model.(map[string]any)
		id, _ := fields["id"].(string)
		inproc = append(inproc, id)
		info := bridge.actions["getModelInfo"].(func() map[string]any)()
		id, _ = info["id"].(string)
		subprocess = append(subprocess, id)
	}
	m.opts.SessionHandle = handle

	m.cycleModel(true)
	if err := m.buildSlashContext(t.Context()).SwitchModel("capture/next"); err != nil {
		t.Fatal(err)
	}
	if len(handle.cycled) != 1 || len(handle.switched) != 1 {
		t.Fatalf("Session got CycleToModel %v and SetModel %v", handle.cycled, handle.switched)
	}
	want := []string{handle.cycled[0].ID, "capture/next"}
	if len(inproc) != 2 || inproc[0] != want[0] || inproc[1] != want[1] {
		t.Errorf("in-process ctx.model during model_select = %q, want %q", inproc, want)
	}
	if len(subprocess) != 2 || subprocess[0] != want[0] || subprocess[1] != want[1] {
		t.Errorf("subprocess getModelInfo during model_select = %q, want %q", subprocess, want)
	}
}

// Pi's ModelRuntime.streamSimple passes an extension's options through unchanged (model-runtime.ts streamSimple): a request without `reasoning` streams without thinking, whatever the Session's level, as headless PiG does. Interactive mode defaulted such a request to the thinking level it held when it wired the bridge, so an extension stream thought at the startup level even after the user turned thinking off.
func TestInteractiveExtensionStreamWithoutReasoningDoesNotThink(t *testing.T) {
	m := modelPickerTestMode(t)
	m.thinkingLevel = string(ai.ThinkingHigh)
	model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "provider"}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}}
	m.opts.ModelBuilder = func(string) (*ai.Model, error) { return model, nil }
	handle := &modelRequestCaptureHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}}
	m.opts.SessionHandle = handle
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	defer detach()

	stream := bridge.actions["streamModel"].(func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error))
	for _, request := range []struct {
		reasoning any
		want      ai.ThinkingLevel
	}{{nil, ""}, {"low", ai.ThinkingLow}} {
		body := map[string]any{"messages": []any{}}
		if request.reasoning != nil {
			body["reasoning"] = request.reasoning
		}
		result, err := stream(t.Context(), map[string]any{"provider": "provider", "modelId": "model"}, body)
		if err != nil {
			t.Fatal(err)
		}
		result.Result()
		if handle.options.Thinking != request.want {
			t.Errorf("reasoning %v streamed with thinking %q, want %q", request.reasoning, handle.options.Thinking, request.want)
		}
	}
}
