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
// every extension. It is the one-client form; a client that takes deliveries, or one of several, attaches through
// Views().Attach. Abilities whose claims or drawing limits are outside the record are refused with code invalid.
func (b *UIBridge) SetClientAbilities(a *uiview.ClientAbilities) *uiview.Error {
	return b.views.SetAbilities(a)
}

// ViewsSessionReplaced ends every mount, as the session they belong to was replaced.
func (b *UIBridge) ViewsSessionReplaced() { b.views.Break("", uiview.ReasonSessionReplaced) }

// SupersedeViews ends every mount of the named extensions as broken with reason owner_gone: a reload replaces their
// instances, and a mount belongs to the instance that made it, as Pi's reload removes everything an extension drew
// before the extensions load again (interactive-mode.ts handleReloadCommand, resetExtensionUI). The host calls it
// before a replacement starts, so the instance that follows mounts its keys under a new epoch. The connection of an
// instance that is being replaced mounts nothing more: a mount made after this point would outlive its instance.
func (b *UIBridge) SupersedeViews(extNames []string) {
	b.mu.Lock()
	for _, name := range extNames {
		if conn := b.extConns[name]; conn != nil {
			b.viewSuperseded[conn] = struct{}{}
		}
	}
	b.mu.Unlock()
	for _, name := range extNames {
		b.views.Break(name, uiview.ReasonOwnerGone)
	}
}

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
	return b.views.Invoke(ctx, extension, ref, nodeKey, intent, arg, viewInvoker{b, extension, conn})
}

// InvokeViewIntentFrom raises an intent for the client that holds attachment t. Once t ended the result is stale.
func (b *UIBridge) InvokeViewIntentFrom(ctx context.Context, t *uiview.Attachment, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result {
	b.mu.RLock()
	conn := b.extConns[extension]
	b.mu.RUnlock()
	if conn == nil {
		return uiview.Result{Outcome: uiview.OutcomeRefused, Error: "extension is not connected"}
	}
	return t.Invoke(ctx, extension, ref, nodeKey, intent, arg, viewInvoker{b, extension, conn})
}

// viewAnswer names the ui.view.intent request an extension is answering: the id is unique on the extension's connection.
type viewAnswer struct{ extension, request string }

// viewInvoker carries intents over the owning extension's connection. While the request is unanswered the bridge knows
// its id, so a ui.view call the handler makes inside it (CallPayload.ParentRequestID) is attributed to the intent.
type viewInvoker struct {
	b         *UIBridge
	extension string
	conn      *Conn
}

func (v viewInvoker) Invoke(ctx context.Context, inv uiview.Invocation) (uiview.Result, error) {
	var reply struct {
		Outcome uiview.Outcome  `json:"outcome"`
		Answer  json.RawMessage `json:"answer"`
		Error   string          `json:"error"`
	}
	answer := viewAnswer{v.extension, v.conn.newRequestID()}
	v.b.viewAnswers.Store(answer, inv.Ticket)
	defer v.b.viewAnswers.Delete(answer)
	if err := requestWithID(ctx, v.conn, answer.request, RequestUIViewIntent, inv, &reply); err != nil {
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
	case CallUIViewMount, CallUIViewUpdate, CallUIViewHide, CallUIViewShow, CallUIViewFinish, CallUIViewRemove, CallUIViewState, CallUIViewDiagnostics:
		return true
	}
	return false
}

// reservedViewPlugin is the plugin name of the host's own mounts of chord surfaces (internal/chordview).
const reservedViewPlugin = "chord"

// answeringTicket returns the ticket of the intent whose handler made call: the one whose ui.view.intent request, still
// unanswered, is the request the call names as its parent. Any other call returns "".
func (b *UIBridge) answeringTicket(extName string, call *CallPayload) string {
	if call.ParentRequestID == "" {
		return ""
	}
	ticket, _ := b.viewAnswers.Load(viewAnswer{extName, call.ParentRequestID})
	s, _ := ticket.(string)
	return s
}

func (b *UIBridge) handleViewCall(extName string, owner *Conn, call *CallPayload) (*CallResultPayload, error) {
	// pig additive (D614): the plugin name chord is the host's; an extension of that name mounts nothing.
	if extName == reservedViewPlugin {
		return viewError(&uiview.Error{Code: uiview.CodeInvalid, Message: "the plugin name " + reservedViewPlugin + " is reserved for the host"})
	}
	if len(bytes.TrimSpace(call.Args)) == 0 {
		return nil, errors.New(call.Method + ": missing arguments")
	}
	switch call.Method {
	case CallUIViewMount:
		a, err := uiview.DecodeMount(call.Args)
		if err != nil {
			return viewError(err)
		}
		b.mu.RLock()
		_, superseded := b.viewSuperseded[owner]
		b.mu.RUnlock()
		if superseded {
			return viewError(&uiview.Error{Code: uiview.CodeTerminal, Message: "a reload is replacing this instance of the extension: it mounts nothing more"})
		}
		a.Answering = b.answeringTicket(extName, call)
		if gerr := checkMountGrids(&a.Content); gerr != nil {
			return viewError(gridCheckError(gerr))
		}
		st, err := b.views.Mount(extName, a)
		if err != nil {
			return viewError(err)
		}
		return viewStatus(st)
	case CallUIViewUpdate:
		a, err := uiview.DecodeUpdate(call.Args)
		if err != nil {
			b.views.Rejected(extName, "update", call.Args, err)
			return viewError(err)
		}
		a.Answering = b.answeringTicket(extName, call)
		if gerr := checkMountGrids(&a.Content); gerr != nil {
			err := gridCheckError(gerr)
			b.views.Rejected(extName, "update", call.Args, err)
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
	case CallUIViewFinish:
		a, err := uiview.DecodeFinish(call.Args)
		if err != nil {
			b.views.Rejected(extName, "finish", call.Args, err)
			return viewError(err)
		}
		st, err := b.views.Finish(extName, a)
		if err != nil {
			return viewError(err)
		}
		return viewStatus(st)
	case CallUIViewDiagnostics:
		a, err := uiview.DecodeState(call.Args)
		if err != nil {
			return viewError(err)
		}
		d, err := b.views.Diagnostics(extName, a)
		if err != nil {
			return viewError(err)
		}
		raw, jerr := json.Marshal(d)
		if jerr != nil {
			return nil, jerr
		}
		return &CallResultPayload{Result: raw}, nil
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
