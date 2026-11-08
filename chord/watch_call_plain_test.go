package chord

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// upstream: packages/chord/test/watch.test.ts "hands a listener plain data only: `{ kind, value, ops, retired }`, cloneable and serializable". Upstream pins Object.keys and a structuredClone and JSON round trip; the Go call is a struct value, so the key list is its field list and the round trip is encoding/json.
func TestWatchCallIsPlainDataOfItsFieldsThatSurvivesAJSONRoundTrip(t *testing.T) {
	for _, call := range []any{WatchCall[any]{}, NativeWatchCall[any]{}} {
		kind := reflect.TypeOf(call)
		var fields []string
		for field := range kind.Fields() {
			fields = append(fields, field.Name)
		}
		want := []string{"Kind", "Value", "Ops", "Retired", "Error"}
		if kind.Name() == "NativeWatchCall" {
			want = []string{"Kind", "Value", "Error"}
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("%s fields = %v, want %v", kind.Name(), fields, want)
		}
	}
	watch, scheduler, _ := recordedWatch(map[string]any{"n": 0.0})
	listener, changes := collectChanges()
	watch.Start(listener)
	ctx := context.Background()
	watch.Advance(ctx, frame(map[string]any{"n": 1.0}, []delta.Op{{"s", []any{"n"}, 1.0}}))
	scheduler.Wait()
	release := scheduler.Hold()
	watch.Advance(ctx, frame(map[string]any{"n": 2.0}, []delta.Op{{"s", []any{"n"}, 2.0}}))
	watch.Advance(ctx, retirement(map[string]any{"n": 2.0}))
	release()
	scheduler.Wait()
	got := changes()
	if len(got) != 2 {
		t.Fatalf("changes = %#v, want 2", got)
	}
	for _, call := range got {
		encoded, err := json.Marshal(call)
		if err != nil {
			t.Fatal(err)
		}
		var decoded WatchCall[any]
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Kind != call.Kind || decoded.Retired != call.Retired || !equalJSON(decoded.Value, call.Value) || !equalJSON(decoded.Ops, call.Ops) {
			t.Fatalf("round trip of %s gave %#v, want %#v", encoded, decoded, call)
		}
	}
	_, _ = watch.Stop()
}
