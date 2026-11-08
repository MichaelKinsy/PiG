package hosts

import (
	"errors"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// A hold its factory refused with `forbidden` is told to the caller, never an owner notice, whatever its message says
// (Pi's isOwnerNotice goes by the error's class: the terminals plugin's "agent terminals are for session workers" has no
// `forbidden` in its text and showed as a server toast).
func TestIsOwnerNoticeGoesByTheErrorClassNotItsText(t *testing.T) {
	broken := func(err error) host.HostError {
		return host.HostError{Kind: host.ErrActivationFailed, Object: "p2.agent-terminals", Failure: data.FailureCause{Kind: data.FailureBroken, Error: data.AsChordError(err, "p2.agent-terminals")}}
	}
	for name, test := range map[string]struct {
		failure host.HostError
		want    bool
	}{
		"a refused hold, its message without the word": {broken(p2runtime.Forbidden("agent terminals are for session workers")), false},
		"a refused hold, as the call's error":          {host.HostError{Kind: host.ErrRoutineFailed, Error: p2runtime.Forbidden("owners only")}, false},
		"a released object":                            {broken(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "gone"}, nil)), false},
		"a route that did not resolve":                 {host.HostError{Kind: host.ErrResolveFailed, Error: errors.New("no host")}, false},
		"a factory that threw":                         {broken(errors.New("boom")), true},
		"a message that says forbidden, another class": {broken(errors.New("forbidden by the disk")), true},
		"a routine that threw":                         {host.HostError{Kind: host.ErrRoutineFailed, Error: errors.New("boom")}, true},
	} {
		if got := isOwnerNotice(test.failure); got != test.want {
			t.Errorf("%s: isOwnerNotice = %v, want %v", name, got, test.want)
		}
	}
}
