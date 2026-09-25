package main

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// sessionExtensionActions returns the extension actions a Session backs in
// every mode. Upstream AgentSession._bindExtensionCore binds sendUserMessage,
// isIdle, abort, and hasPendingMessages from the session whichever mode runs
// it. Session owns command actions such as waitForIdle; current returns the
// session, or nil while it is being created.
func sessionExtensionActions(current func() *coding.Session) (extension.ExtensionActions, extension.ContextActions) {
	actions := extension.ExtensionActions{
		SendUserMessage: func(content any, opts *extension.SendUserMessageOptions) error {
			var deliverAs extension.DeliverAs
			if opts != nil {
				deliverAs = opts.DeliverAs
			}
			return sendSessionUserMessage(current(), content, deliverAs)
		},
	}
	contextActions := extension.ContextActions{
		IsIdle: func() bool {
			sess := current()
			return sess == nil || sess.IsIdle()
		},
		Abort: func() {
			if sess := current(); sess != nil {
				sess.RequestAbort()
			}
		},
		HasPendingMessages: func() bool {
			sess := current()
			return sess != nil && sess.HasPendingMessages()
		},
	}
	return actions, contextActions
}

func sendSessionUserMessage(sess *coding.Session, content any, deliverAs extension.DeliverAs) error {
	if sess == nil {
		return errSessionNotReady
	}
	return sess.SendUserMessage(content, deliverAs)
}

// errSessionNotReady rejects a session action before the session exists.
var errSessionNotReady = errors.New("agent session not initialized")

// bindSessionExtensionActions binds the session-backed actions for in-process
// extensions (runner) and subprocess extensions (bridge host actions).
// contextActions carries the mode's other context actions.
func bindSessionExtensionActions(runner *inproc.Runner, bridge *subprocess.UIBridge, current func() *coding.Session, contextActions extension.ContextActions) {
	actions, sessionContext := sessionExtensionActions(current)
	if runner != nil {
		contextActions.IsIdle = sessionContext.IsIdle
		contextActions.Abort = sessionContext.Abort
		contextActions.HasPendingMessages = sessionContext.HasPendingMessages
		runner.BindCore(actions, contextActions, nil)
	}
	if bridge == nil {
		return
	}
	bridge.SetHostAction("sendUserMessage", func(content any, opts subprocess.SendUserMessageOptions) error {
		return sendSessionUserMessage(current(), content, extension.DeliverAs(opts.DeliverAs))
	})
	bridge.SetHostAction("isIdle", sessionContext.IsIdle)
	bridge.SetHostAction("abort", sessionContext.Abort)
	bridge.SetHostAction("hasPendingMessages", sessionContext.HasPendingMessages)
	// exec is mode-independent in upstream: loader.ts's createExtensionContext
	// binds ExtensionContext.exec the same way for every mode (print, JSON,
	// RPC, interactive). Bind it once here so print, JSON, and RPC mode share
	// the same implementation instead of each mode wiring its own; interactive
	// mode binds the identical extension.ExecCommand call directly against its
	// session in wireSubprocessHostCallbacks.
	bridge.SetHostAction("exec", func(command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
		cwd := ""
		if sess := current(); sess != nil {
			cwd = sess.CWD()
		}
		return extension.ExecCommand(context.Background(), cwd, command, args, opts)
	})
	bridge.BindCommandActions(extension.CommandActions{
		WaitForIdle: func() error {
			if sess := current(); sess != nil {
				return sess.ExtensionCommandActions().WaitForIdle()
			}
			return nil
		},
		NavigateTree: func(targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return sess.ExtensionCommandActions().NavigateTree(targetID, opts)
			}
			return extension.CancelledResult{}, errSessionNotReady
		},
	})
}
