package durableadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// viewFrames is the authoritative Chord source of one conversation view. It republishes the conversation's own view state (conversation.viewState, view.ts:92-104), whose CommittedStateSource frames carry each durable revision's exact operations and are buffered without bound behind a slow consumer (observation.ts:SessionSourceAttachment.publish). The snapshot is the durable state's publication at subscription; each later publication becomes the next cursor with its own operations, and the view's strict JSON form advances by them, sharing every unchanged subtree, so a revision costs its operations, not the size of the transcript. It has one attachment, the state ViewState returns; disposing that attachment disposes the durable state, releasing the mount.
type viewFrames struct {
	state       durable.AttachedReplicatedState[harness.ConversationView]
	unsubscribe func()

	mu       sync.Mutex
	snapshot chord.ReplicatedStateSourceSnapshot
	value    chord.JsonValue
	// sequence is the durable state's publication sequence that value reflects.
	sequence int
	cursor   int
	frames   []chord.ReplicatedStateSourceFrame
	listener func(chord.ReplicatedStateSourceFrame)
	attached bool
	// delivering is set while one goroutine drains frames, so frames reach the listener once each, in cursor order.
	delivering bool
	failed     bool
	disposed   bool
}

var (
	_ chord.ReplicatedStateSource           = (*viewFrames)(nil)
	_ chord.ReplicatedStateSourceAttachment = (*viewFrames)(nil)
)

// attachViewFrames attaches to the conversation's view state. ctx bounds only the attachment, as upstream's viewState context (view.ts:92-104, the attach at :124-151).
func attachViewFrames(ctx context.Context, conversation harness.Conversation) (*viewFrames, error) {
	state, err := conversation.ViewState(ctx)
	if err != nil {
		return nil, err
	}
	source := &viewFrames{state: state}
	// Subscribe before the snapshot so no publication falls between them; receive skips the publications the snapshot already holds, and waits for the snapshot under mu.
	source.mu.Lock()
	source.unsubscribe = state.SubscribeSource(source.receive)
	typed, sequence := state.InternalSnapshot()
	value, err := chordjson.Stored(typed)
	if err != nil {
		source.disposed = true
		source.mu.Unlock()
		source.unsubscribe()
		state.Dispose()
		return nil, err
	}
	source.value = value
	source.sequence = sequence
	source.snapshot = chord.ReplicatedStateSourceSnapshot{Value: value}
	source.mu.Unlock()
	return source, nil
}

// Attach returns the source's only attachment.
func (source *viewFrames) Attach() chord.ReplicatedStateSourceAttachment {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.attached {
		panic(errors.New("Conversation view source is already attached"))
	}
	source.attached = true
	return source
}

func (source *viewFrames) Snapshot() chord.ReplicatedStateSourceSnapshot { return source.snapshot }

// Activate installs the listener and delivers the frames buffered since the snapshot.
func (source *viewFrames) Activate(listener func(chord.ReplicatedStateSourceFrame)) {
	source.mu.Lock()
	if source.listener != nil {
		source.mu.Unlock()
		panic(errors.New("Conversation view attachment is already active"))
	}
	source.listener = listener
	source.mu.Unlock()
	source.drain()
}

// receive is the durable state's publication listener. A publication whose operations do not apply to the previous revision breaks the view's contract: the source stops and releases the durable state, and the Chord state keeps its last revision.
func (source *viewFrames) receive(ops []durable.Op, sequence int, ctx context.Context) {
	source.mu.Lock()
	if source.disposed || source.failed || sequence <= source.sequence {
		source.mu.Unlock()
		return
	}
	var batch []chord.Op
	var value chord.JsonValue
	var err error
	if sequence != source.sequence+1 {
		err = fmt.Errorf("publication sequence has a gap: expected %d, received %d", source.sequence+1, sequence)
	} else if batch, err = strictOps(ops); err == nil {
		value, err = delta.ApplyImmutable(source.value, batch)
	}
	if err != nil {
		source.failed = true
		source.frames = nil
		source.mu.Unlock()
		fmt.Fprintln(os.Stderr, "Transcript publication failed:", fmt.Errorf("conversation view revision %d: %w", source.cursor+1, err))
		source.unsubscribe()
		source.state.Dispose()
		return
	}
	source.value = value
	source.sequence = sequence
	source.cursor++
	source.frames = append(source.frames, chord.ReplicatedStateSourceFrame{Cursor: source.cursor, Value: value, Ops: batch, Context: ctx})
	source.mu.Unlock()
	source.drain()
}

// strictOps returns the operations in Chord's strict JSON form. Durable view operations may carry typed records (an appended entry, a set document), which are converted.
func strictOps(ops []durable.Op) ([]chord.Op, error) {
	batch := make([]chord.Op, len(ops))
	for index, op := range ops {
		if chordjson.IsValue([]any(op)) {
			batch[index] = chord.Op(op)
			continue
		}
		stored, err := chordjson.Stored(op)
		if err != nil {
			return nil, err
		}
		batch[index] = chord.Op(stored.([]any))
	}
	return batch, nil
}

func (source *viewFrames) drain() {
	source.mu.Lock()
	if source.delivering || source.listener == nil {
		source.mu.Unlock()
		return
	}
	source.delivering = true
	finished := false
	defer func() {
		// A panicking listener left the lock released.
		if !finished {
			source.mu.Lock()
			source.delivering = false
			source.mu.Unlock()
		}
	}()
	for !source.disposed && len(source.frames) > 0 {
		frame := source.frames[0]
		source.frames = source.frames[1:]
		listener := source.listener
		source.mu.Unlock()
		listener(frame)
		source.mu.Lock()
	}
	source.delivering = false
	finished = true
	source.mu.Unlock()
}

// Dispose stops publication and disposes the durable state, which releases the view mount with its last observer.
func (source *viewFrames) Dispose() {
	source.mu.Lock()
	if source.disposed {
		source.mu.Unlock()
		return
	}
	source.disposed = true
	source.frames = nil
	source.mu.Unlock()
	source.unsubscribe()
	source.state.Dispose()
}
