package inproc

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestUnboundCommandContextSessionActionsReportNotCancelled ports runner.ts:383-386 and bindCommandContext
// (runner.ts:557-562): a runner whose command context was never bound answers newSession, fork, navigateTree and
// switchSession with { cancelled: false }, so a command that runs before binding is not told its action was cancelled.
// Once actions are bound, their own result wins (runner.ts:547-554).
func TestUnboundCommandContextSessionActionsReportNotCancelled(t *testing.T) {
	runner := NewRunner(nil, t.TempDir())
	command := runner.CreateCommandContext()
	results := map[string]func() (extension.CancelledResult, error){
		"newSession":    func() (extension.CancelledResult, error) { return command.NewSession(nil) },
		"fork":          func() (extension.CancelledResult, error) { return command.Fork("entry", nil) },
		"navigateTree":  func() (extension.CancelledResult, error) { return command.NavigateTree("entry", nil) },
		"switchSession": func() (extension.CancelledResult, error) { return command.SwitchSession("/s.jsonl", nil) },
	}
	for name, call := range results {
		if result, err := call(); err != nil || result.Cancelled {
			t.Errorf("unbound %s = %+v, %v; want {cancelled:false}", name, result, err)
		}
	}

	runner.BindCommandActions(extension.CommandActions{
		NewSession: func(*extension.NewSessionOptions) (extension.CancelledResult, error) {
			return extension.CancelledResult{Cancelled: true}, nil
		},
		SwitchSession: func(string, *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			return extension.CancelledResult{Cancelled: true}, nil
		},
	})
	bound := runner.CreateCommandContext()
	if result, _ := bound.NewSession(nil); !result.Cancelled {
		t.Error("a bound newSession result was not returned")
	}
	if result, _ := bound.SwitchSession("/s.jsonl", nil); !result.Cancelled {
		t.Error("a bound switchSession result was not returned")
	}
}
