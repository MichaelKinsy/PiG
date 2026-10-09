package chord

import (
	"errors"
	"strings"
	"testing"
)

// failingAttachment is a source attachment whose snapshot, activate or dispose throws, as a panic does in Go.
type failingAttachment struct {
	mode string
	log  *[]string
}

func (attachment failingAttachment) Snapshot() ReplicatedStateSourceSnapshot {
	*attachment.log = append(*attachment.log, "snapshot")
	if strings.HasPrefix(attachment.mode, "snapshot") {
		panic(errors.New("snapshot boom"))
	}
	return ReplicatedStateSourceSnapshot{Value: map[string]any{"n": 0.0}}
}

func (attachment failingAttachment) Activate(listener func(ReplicatedStateSourceFrame)) {
	*attachment.log = append(*attachment.log, "activate")
	if strings.HasPrefix(attachment.mode, "activate") {
		listener(ReplicatedStateSourceFrame{Value: map[string]any{"n": 1.0}, Ops: []Op{{"s", []any{"n"}, 1.0}}, Cursor: 1})
		panic(errors.New("activate boom"))
	}
}

func (attachment failingAttachment) Dispose() {
	*attachment.log = append(*attachment.log, "dispose")
	if strings.HasSuffix(attachment.mode, "+dispose") {
		panic(errors.New("dispose boom"))
	}
}

type failingSource struct {
	mode string
	log  *[]string
}

func (source failingSource) Attach() ReplicatedStateSourceAttachment {
	*source.log = append(*source.log, "attach")
	return failingAttachment(source)
}

// Pi packages/chord/src/services/state.ts:324-341 attachReplicatedStateSource constructs and activates the state inside one try: when the
// snapshot read (state.ts:255) or activate (state.ts:276) throws, it disposes the attachment and rethrows, or throws AggregateError "Failed to
// attach replicated state source" over both failures when dispose throws too. A Go panic from the attachment is the thrown error, as
// disposeAttachment already treats it. Expected results and call logs are the Pi oracle's (node over .upstream/current state.ts):
//
//	snapshot           Error: snapshot boom | attach, snapshot, dispose
//	activate           Error: activate boom | attach, snapshot, activate, dispose
//	activate+dispose   AggregateError: Failed to attach replicated state source [activate boom, dispose boom] | attach, snapshot, activate, dispose
//	snapshot+dispose   AggregateError: Failed to attach replicated state source [snapshot boom, dispose boom] | attach, snapshot, dispose
func TestAttachReplicatedStateDisposesWhenSnapshotOrActivateThrowsAsPi(t *testing.T) {
	for _, test := range []struct {
		mode, want, log string
	}{
		{"snapshot", "snapshot boom", "attach, snapshot, dispose"},
		{"activate", "activate boom", "attach, snapshot, activate, dispose"},
		{"activate+dispose", "Failed to attach replicated state source [activate boom, dispose boom]", "attach, snapshot, activate, dispose"},
		{"snapshot+dispose", "Failed to attach replicated state source [snapshot boom, dispose boom]", "attach, snapshot, dispose"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			var log []string
			var got string
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						got = "panic: " + recoveredError(recovered).Error()
					}
				}()
				state, err := AttachReplicatedState[docState](failingSource{mode: test.mode, log: &log}, ReplicatedStateSourceOptions{OnError: func(err error) {
					log = append(log, "onError "+err.Error())
				}})
				switch aggregate, ok := errors.AsType[*AggregateError](err); {
				case state != nil || err == nil:
					got = "attached"
				case ok:
					messages := make([]string, len(aggregate.Errors))
					for i, cause := range aggregate.Errors {
						messages[i] = cause.(error).Error()
					}
					got = aggregate.Message + " [" + strings.Join(messages, ", ") + "]"
				default:
					got = err.Error()
				}
			}()
			if got != test.want || strings.Join(log, ", ") != test.log {
				t.Errorf("got %q | %s\nwant %q | %s", got, strings.Join(log, ", "), test.want, test.log)
			}
		})
	}
}
