// Ports packages/coding-agent/src/core/agent-session-runtime.ts
package coding

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// SetRebindSession installs the awaited mode rebind for Session-owned replacements. The callback renders and subscribes to the destination before dispatching its session_start event.
func (s *Session) SetRebindSession(rebind func(context.Context, extension.SessionStartEvent) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebindSession = rebind
}

// SetBeforeSessionReplacement installs the mode's awaited drain after cancellable before hooks approve replacement and before shutdown is emitted.
func (s *Session) SetBeforeSessionReplacement(drain func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeSessionReplacement = drain
}

func (s *Session) beforeExtensionReplacement(ctx context.Context, event any) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	extension.CallInitiated(ctx)
	runner := s.currentRunner()
	if runner == nil {
		return false, nil
	}
	result, err := runner.Emit(ctx, event)
	if err != nil {
		return false, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	var value struct {
		Cancel bool `json:"cancel"`
	}
	if err := json.Unmarshal(encoded, &value); err != nil {
		return false, err
	}
	return value.Cancel, ctx.Err()
}

func (s *Session) finishExtensionReplacement(ctx context.Context, next *icodingagent.Session, reason string) (extension.CancelledResult, error) {
	s.mu.Lock()
	drain := s.beforeSessionReplacement
	s.mu.Unlock()
	if drain != nil {
		if err := drain(ctx); err != nil {
			return extension.CancelledResult{}, err
		}
	}
	if err := s.Abort(ctx); err != nil {
		return extension.CancelledResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return extension.CancelledResult{}, err
	}
	previous := s.Path()
	if runner := s.currentRunner(); runner != nil {
		if _, err := runner.Emit(ctx, extension.SessionShutdownEvent{Type: "session_shutdown", Reason: reason, TargetSessionFile: next.Path()}); err != nil {
			return extension.CancelledResult{}, err
		}
	}
	// pig divergence (D30): session replacement retains the host-scoped runner.
	s.ReplaceInner(next)
	event := extension.SessionStartEvent{Type: "session_start", Reason: reason, PreviousSessionFile: previous}
	s.mu.Lock()
	rebind := s.rebindSession
	s.mu.Unlock()
	if rebind != nil {
		return extension.CancelledResult{}, rebind(ctx, event)
	}
	if runner := s.currentRunner(); runner != nil {
		_, err := runner.Emit(ctx, event)
		return extension.CancelledResult{}, err
	}
	return extension.CancelledResult{}, nil
}

func (s *Session) extensionNewSession(ctx context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
	cancelled, err := s.beforeExtensionReplacement(ctx, extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "new"})
	if err != nil || cancelled {
		return extension.CancelledResult{Cancelled: cancelled}, err
	}
	parent := ""
	if opts != nil {
		parent = opts.ParentSession
	}
	next, err := s.prepareNewSession(parent)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	return s.finishExtensionReplacement(ctx, next, "new")
}

func (s *Session) extensionSwitchSession(ctx context.Context, path string, _ *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	cancelled, err := s.beforeExtensionReplacement(ctx, extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "resume", TargetSessionFile: path})
	if err != nil || cancelled {
		return extension.CancelledResult{Cancelled: cancelled}, err
	}
	next, err := newSessionManagerForDir(s.services, s.sessionDir).Load(path)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	return s.finishExtensionReplacement(ctx, next, "resume")
}

func (s *Session) extensionFork(ctx context.Context, entryID string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
	position := "before"
	if opts != nil && opts.Position != "" {
		position = opts.Position
	}
	cancelled, err := s.beforeExtensionReplacement(ctx, extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: entryID, Position: position})
	if err != nil || cancelled {
		return extension.CancelledResult{Cancelled: cancelled}, err
	}
	entry, ok := s.inner.EntryByID(entryID)
	if !ok {
		return extension.CancelledResult{}, errors.New("Invalid entry ID for forking")
	}
	leaf := &entryID
	if position != "at" {
		message, isMessage := entry.AsMessage()
		if !isMessage || message.Message.User == nil {
			return extension.CancelledResult{}, errors.New("Invalid entry ID for forking")
		}
		leaf = entry.Base.ParentID
	}
	var next *icodingagent.Session
	switch {
	case s.noSession:
		id, idErr := icodingagent.GenerateSessionID()
		if idErr != nil {
			return extension.CancelledResult{}, idErr
		}
		next = icodingagent.NewSession(id, s.services.CWD())
		if leaf != nil {
			for _, item := range s.inner.Branch(*leaf) {
				if err := next.AppendEntry(item); err != nil {
					return extension.CancelledResult{}, err
				}
			}
		}
	case leaf == nil:
		id, idErr := icodingagent.GenerateSessionID()
		if idErr != nil {
			return extension.CancelledResult{}, idErr
		}
		next, err = newSessionManagerForDir(s.services, s.sessionDir).Create(id, s.Path())
	default:
		if err := s.inner.CheckSavedForFork(); err != nil {
			return extension.CancelledResult{}, err
		}
		next, err = newSessionManagerForDir(s.services, s.sessionDir).Clone(s.inner, *leaf)
	}
	if err != nil {
		return extension.CancelledResult{}, err
	}
	return s.finishExtensionReplacement(ctx, next, "fork")
}
