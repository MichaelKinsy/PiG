package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// pig additive (D614): plugin UI mounts. The calls below carry the uiview registry's records; the registry owns the
// lifecycle, and an extension is its owner by name. A client attaches through [UIBridge.SetClientAbilities] and draws
// the snapshots [UIBridge.Views] publishes.

// knownViewKind reports whether kind is in the D107 view vocabulary: the first set and every registered widget.
func knownViewKind(kind string) bool {
	switch kind {
	case ViewKindContainer, ViewKindBox, ViewKindText, ViewKindTruncatedText, ViewKindMarkdown, ViewKindSpacer, ViewKindDynamicBorder,
		ViewKindSelectList, ViewKindSettingsList, ViewKindImage, ViewKindLoader, ViewKindHStack, ViewKindVStack, ViewKindLines,
		ViewKindUserMessage, ViewKindAssistantMessage, ViewKindToolExecution, ViewKindBashExecution, ViewKindDiff:
		return true
	}
	return viewWidgets[kind] != nil
}

// Views returns the mount registry a client subscribes to.
func (b *UIBridge) Views() *uiview.Registry { return b.views }

// SetClientAbilities attaches a client that draws plugin mounts, or detaches it with nil, and replicates the abilities to
// every extension.
func (b *UIBridge) SetClientAbilities(a *uiview.ClientAbilities) { b.views.SetAbilities(a) }

// ViewsSessionReplaced ends every mount, as the session they belong to was replaced.
func (b *UIBridge) ViewsSessionReplaced() { b.views.Break("", uiview.ReasonSessionReplaced) }

// ViewsShutdown ends every mount, as the host is shutting down.
func (b *UIBridge) ViewsShutdown() { b.views.Break("", uiview.ReasonShutdown) }

// InvokeViewIntent raises a user's intent on a mount of extension and returns its final result.
func (b *UIBridge) InvokeViewIntent(ctx context.Context, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result {
	b.mu.RLock()
	conn := b.extConns[extension]
	b.mu.RUnlock()
	if conn == nil {
		return uiview.Result{Outcome: uiview.OutcomeRefused, Error: "extension is not connected"}
	}
	return b.views.Invoke(ctx, extension, ref, nodeKey, intent, arg, viewInvoker{conn})
}

// viewInvoker carries intents over the owning extension's connection.
type viewInvoker struct{ conn *Conn }

func (v viewInvoker) Invoke(ctx context.Context, inv uiview.Invocation) (uiview.Result, error) {
	var reply struct {
		Outcome uiview.Outcome  `json:"outcome"`
		Answer  json.RawMessage `json:"answer"`
		Error   string          `json:"error"`
	}
	if err := requestWithID(ctx, v.conn, "", RequestUIViewIntent, inv, &reply); err != nil {
		return uiview.Result{}, err
	}
	return uiview.Result{Ticket: inv.Ticket, Outcome: reply.Outcome, Answer: reply.Answer, Error: reply.Error}, nil
}

func viewError(err *uiview.Error) (*CallResultPayload, error) {
	return &CallResultPayload{Error: &ErrorInfo{Code: string(err.Code), Message: err.Message}}, nil
}

func viewStatus(st uiview.Status) (*CallResultPayload, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: raw}, nil
}

// isViewCall reports whether method is one of the ui.view.* mount calls.
func isViewCall(method string) bool {
	switch method {
	case CallUIViewMount, CallUIViewUpdate, CallUIViewHide, CallUIViewShow, CallUIViewFinish, CallUIViewRemove, CallUIViewState:
		return true
	}
	return false
}

func (b *UIBridge) handleViewCall(extName string, call *CallPayload) (*CallResultPayload, error) {
	if len(bytes.TrimSpace(call.Args)) == 0 {
		return nil, errors.New(call.Method + ": missing arguments")
	}
	switch call.Method {
	case CallUIViewMount:
		a, err := uiview.DecodeMount(call.Args)
		if err != nil {
			return viewError(err)
		}
		st, err := b.views.Mount(extName, a)
		if err != nil {
			return viewError(err)
		}
		return viewStatus(st)
	case CallUIViewUpdate:
		a, err := uiview.DecodeUpdate(call.Args)
		if err != nil {
			return viewError(err)
		}
		st, err := b.views.Update(extName, a)
		if err != nil {
			return viewError(err)
		}
		return viewStatus(st)
	case CallUIViewState:
		a, err := uiview.DecodeState(call.Args)
		if err != nil {
			return viewError(err)
		}
		st, err := b.views.State(extName, a)
		if err != nil {
			return viewError(err)
		}
		return viewStatus(st)
	}
	a, err := uiview.DecodeRef(call.Args)
	if err != nil {
		return viewError(err)
	}
	st, err := b.views.Transition(extName, call.Method[len("ui.view."):], a)
	if err != nil {
		return viewError(err)
	}
	return viewStatus(st)
}
