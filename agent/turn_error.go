package agent

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// TurnFailure is a failed turn, as a Go error.
type TurnFailure struct {
	// Message is what the model call said went wrong. It can be empty.
	Message string
}

// Error implements error.
func (e *TurnFailure) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("turn failed: %s", e.Message)
	}
	return "turn failed"
}

// TurnError turns a failed turn into a Go error.
//
// Send never reports a failed model call through its error value. The
// failure lands on the last assistant message in the slice it returns,
// with StopReason "error". TurnError reads that message back for you:
//
//	msgs, err := sess.Send(ctx, prompt)
//	if err != nil {
//		return err
//	}
//	if err := TurnError(msgs); err != nil {
//		return err // the model call failed; the reason is in err
//	}
//
// It returns nil for turns that finished normally — a regular stop, a
// stop at the length limit, a turn you cancelled, and a retry that failed
// once and then succeeded. Given a whole transcript it only checks the
// last assistant message, i.e. the newest turn.
func TurnError(messages []AgentMessage) error {
	for i := len(messages) - 1; i >= 0; i-- {
		if a := messages[i].Assistant; a != nil {
			if a.StopReason == ai.StopReasonError {
				return &TurnFailure{Message: a.ErrorMessage}
			}
			return nil
		}
	}
	return nil
}
