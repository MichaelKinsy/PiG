package extensionconformance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// nodeFailureDepth is the nesting from which a Node handler's JSON.stringify of a payload can fail at all: below it the recursive stringify has the stack on every Node the row runs on (4458
// levels is the least, Node 24's), and the payload adds eight levels to the value.
const nodeFailureDepth = 9000

// carriesDeepValue reports whether the event holds the nested value (the first update of a stream holds none yet): its JSON is at least two bytes a level long.
func carriesDeepValue(t *testing.T, event any, depth int) bool {
	t.Helper()
	whole, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return len(whole) >= 2*depth
}

// nodeExpectedFailures is where the Node SDK's handlers fail on each event, by a rule that uses nothing of runtime-node's event-stream code (nodeEventStreamOutcomes replays that code, so it
// cannot be the oracle of its own copy): probe is the probe's JSON.stringify of the whole payload, fixture the fixture's own handler's of a tool update's arguments.
//   - A handler fails only on an event of at least nodeFailureDepth levels that carries the deep value; the fixture's only when the event's arguments hold `meta`.
//   - Where the running Node's plain JSON.stringify overflows at that depth (Node 24) every such event fails.
//   - Where it does not (Node 26: its fast path never overflows), a handler fails on such an event only when the stream's replica holds a string an `append` operation made since it was last
//     set (V8 flattens nothing it concatenated, and the recursive path takes over), which the host's own frames say: the paths appended to, and not set again, per stream key.
func nodeExpectedFailures(t *testing.T, events []extension.ExtensionEvent, depths []int, frames [][]byte) (probe, fixture []bool) {
	t.Helper()
	deepValue := strings.Repeat("[", nodeFailureDepth+8) + "1" + strings.Repeat("]", nodeFailureDepth+8)
	plainOverflows := nodeStringifyOverflows(t, []string{deepValue})[0]
	if len(frames) == 0 || len(frames)%len(events) != 0 {
		t.Fatalf("the host wrote %d request frames for %d events, want the same number for each", len(frames), len(events))
	}
	per := len(frames) / len(events)
	appended := map[string][][]any{} // stream key -> the paths whose string an append made
	probe, fixture = make([]bool, len(events)), make([]bool, len(events))
	for index, event := range events {
		var key string
		for _, frame := range frames[index*per : (index+1)*per] {
			var env subprocess.Envelope
			if err := sdkjson.Unmarshal(frame, &env); err != nil || env.Request == nil {
				t.Fatalf("event %d: a request frame does not decode: %v", index, err)
			}
			stream := env.Request.Stream
			if stream == nil {
				t.Fatalf("event %d: a request frame carries no event stream", index)
			}
			key = stream.Key
			if len(env.Request.Args) > 0 {
				delete(appended, key) // a keyframe: every string is the one the frame holds
			}
			if len(stream.Ops) == 0 {
				continue
			}
			var ops []struct {
				Op   string `json:"op"`
				Path []any  `json:"path"`
			}
			if err := sdkjson.Unmarshal(stream.Ops, &ops); err != nil {
				t.Fatalf("event %d: the operations of a frame do not decode: %v", index, err)
			}
			for _, op := range ops {
				kept := appended[key][:0:0]
				for _, path := range appended[key] {
					if !hasPathPrefix(path, op.Path) {
						kept = append(kept, path)
					}
				}
				if op.Op == "append" {
					kept = append(kept, op.Path)
				}
				appended[key] = kept
			}
		}
		if depths[index] < nodeFailureDepth || !carriesDeepValue(t, event, depths[index]) {
			continue
		}
		update, isUpdate := event.(extension.ToolExecutionUpdateEvent)
		args, _ := update.Args.(map[string]any)
		holdsArguments := isUpdate && args["meta"] != nil
		if plainOverflows {
			probe[index], fixture[index] = true, holdsArguments
			continue
		}
		for _, path := range appended[key] {
			probe[index] = true
			if len(path) > 0 && fmt.Sprint(path[0]) == "args" {
				fixture[index] = holdsArguments
			}
		}
	}
	return probe, fixture
}

// hasPathPrefix reports whether path is prefix or lies under it.
func hasPathPrefix(path, prefix []any) bool {
	if len(prefix) > len(path) {
		return false
	}
	for index := range prefix {
		if fmt.Sprint(path[index]) != fmt.Sprint(prefix[index]) {
			return false
		}
	}
	return true
}
