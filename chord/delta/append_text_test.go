package delta

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// upstream: packages/chord/test/delta-append-text.test.ts (#9265): streaming writers appended to growing strings by assignment, and the tracker's prefix check read the whole string on every change.

func appendTextDocument() *JsonObject {
	return chordjson.ObjectOf(
		"title", "t",
		"content", []any{chordjson.ObjectOf("type", "text", "text", "hello", "tags", []any{"a"})},
		"meta", chordjson.ObjectOf("note", "n"),
	)
}

// appendTextCommit runs mutate in one change, adopts it, and returns its ops; it checks that replaying the ops gives the new value.
func appendTextCommit(t *testing.T, tracker *Tracker, mutate func(root *Object)) []Op {
	t.Helper()
	base := tracker.Value()
	change := tracker.BeginChange()
	mutate(change.State())
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if replayed := mustApplyImmutable(t, base, prepared.Ops()); !reflect.DeepEqual(canon(t, replayed), canon(t, prepared.Value())) {
		t.Fatalf("replaying %v gives %v, want %v", prepared.Ops(), mustJSON(t, replayed), mustJSON(t, prepared.Value()))
	}
	if err := tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
	return prepared.Ops()
}

func appendTextBlockText(t *testing.T, tracker *Tracker, index int) string {
	t.Helper()
	return obj(t, arr(t, tracker.Value().Value("content"))[index]).Value("text").(string)
}

func mustAppend(t *testing.T, object *Object, key, text string) {
	t.Helper()
	if err := AppendText(object, key, text); err != nil {
		t.Fatal(err)
	}
}

func TestAppendTextRecordsAnAppendOpAndTheAppendedValue(t *testing.T) {
	tracker := Track(appendTextDocument())
	before := tracker.Value()
	got := appendTextCommit(t, tracker, func(root *Object) { mustAppend(t, root.Array("content").Object(0), "text", " world") })
	if want := []Op{{"a", []any{"content", 0, "text"}, " world"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
	if text := appendTextBlockText(t, tracker, 0); text != "hello world" {
		t.Fatalf("text = %q", text)
	}
	if text := obj(t, arr(t, before.Value("content"))[0]).Value("text"); text != "hello" {
		t.Fatalf("the earlier revision changed: %q", text)
	}
	got = appendTextCommit(t, tracker, func(root *Object) { mustAppend(t, root, "title", "!") })
	if want := []Op{{"a", []any{"title"}, "!"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
	got = appendTextCommit(t, tracker, func(root *Object) { mustAppend(t, root.Object("meta"), "note", "ote") })
	if want := []Op{{"a", []any{"meta", "note"}, "ote"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
	want := chordjson.ObjectOf(
		"title", "t!",
		"content", []any{chordjson.ObjectOf("type", "text", "text", "hello world", "tags", []any{"a"})},
		"meta", chordjson.ObjectOf("note", "note"),
	)
	if !reflect.DeepEqual(canon(t, tracker.Value()), canon(t, want)) {
		t.Fatalf("value = %s", mustJSON(t, tracker.Value()))
	}
}

func TestAppendTextJoinsSeveralAppendsToOneKeyIntoOneOpNextToOtherChanges(t *testing.T) {
	tracker := Track(appendTextDocument())
	got := appendTextCommit(t, tracker, func(root *Object) {
		block := root.Array("content").Object(0)
		mustAppend(t, block, "text", " a")
		must(t, root.Set("title", "new"))
		mustAppend(t, block, "text", " b")
		mustAppend(t, block, "text", "")
		mustAppend(t, block, "text", " c")
	})
	if len(got) != 2 {
		t.Fatalf("ops = %v, want two", got)
	}
	for _, want := range []Op{{"a", []any{"content", 0, "text"}, " a b c"}, {"s", []any{"title"}, "new"}} {
		found := false
		for _, op := range got {
			found = found || reflect.DeepEqual(op, want)
		}
		if !found {
			t.Fatalf("ops = %v lack %v", got, want)
		}
	}
	if text := appendTextBlockText(t, tracker, 0); text != "hello a b c" {
		t.Fatalf("text = %q", text)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestAppendTextFallsBackToTheStringComparisonWhenTheSameKeyIsAlsoAssignedOrDeleted(t *testing.T) {
	tracker := Track(appendTextDocument())
	for _, step := range []struct {
		name   string
		mutate func(root *Object)
		want   []Op
	}{
		{"assigned after", func(root *Object) {
			block := root.Array("content").Object(0)
			mustAppend(t, block, "text", " x")
			must(t, block.Set("text", block.Get("text").(string)+" y"))
		}, []Op{{"a", []any{"content", 0, "text"}, " x y"}}},
		{"replaced after", func(root *Object) {
			block := root.Array("content").Object(0)
			mustAppend(t, block, "text", " z")
			must(t, block.Set("text", "replaced"))
		}, []Op{{"s", []any{"content", 0, "text"}, "replaced"}}},
		{"appended after an assignment", func(root *Object) {
			must(t, root.Set("title", "assigned"))
			mustAppend(t, root, "title", " then appended")
		}, []Op{{"s", []any{"title"}, "assigned then appended"}}},
		{"deleted after", func(root *Object) {
			meta := root.Object("meta")
			mustAppend(t, meta, "note", "x")
			meta.Delete("note")
		}, []Op{{"d", []any{"meta", "note"}}}},
	} {
		if got := appendTextCommit(t, tracker, step.mutate); !reflect.DeepEqual(got, step.want) {
			t.Fatalf("%s: ops = %v, want %v", step.name, got, step.want)
		}
	}
}

func TestAppendTextAppendsInsideABlockTheSameChangeInsertedOrReplaced(t *testing.T) {
	tracker := Track(appendTextDocument())
	appendTextCommit(t, tracker, func(root *Object) {
		content := root.Array("content")
		_, err := content.Push(chordjson.ObjectOf("type", "text", "text", "new", "tags", []any{}))
		must(t, err)
		mustAppend(t, content.Object(1), "text", " block")
		must(t, content.Set(0, chordjson.ObjectOf("type", "text", "text", "other", "tags", []any{})))
		mustAppend(t, content.Object(0), "text", " one")
	})
	if first, second := appendTextBlockText(t, tracker, 0), appendTextBlockText(t, tracker, 1); first != "other one" || second != "new block" {
		t.Fatalf("texts = %q, %q", first, second)
	}
}

func TestAppendTextRejectsWhatACallerCanGetWrong(t *testing.T) {
	tracker := Track(appendTextDocument())
	change := tracker.BeginChange()
	state := change.State()
	for _, key := range []string{"content", "missing", "meta"} {
		if err := AppendText(state, key, "x"); err == nil || !strings.HasPrefix(err.Error(), "appendText() needs a string at \""+key+"\"") {
			t.Fatalf("AppendText(%q) = %v", key, err)
		}
	}
	block := state.Array("content").Object(0)
	if _, err := change.Prepare(); err != nil {
		t.Fatal(err)
	}
	expectPanic(t, ErrRevoked, func() { _ = AppendText(block, "text", "x") })
	aborted := tracker.BeginChange()
	abortedState := aborted.State()
	aborted.Abort()
	expectPanic(t, ErrRevoked, func() { _ = AppendText(abortedState, "title", "x") })
}

func expectPanic(t *testing.T, want error, run func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if err, _ := recovered.(error); !errors.Is(err, want) {
			t.Fatalf("panic = %v, want %v", recovered, want)
		}
	}()
	run()
}

// A text appended through AppendText is the caller's text and not a view of a larger one: the op and the new value own their bytes.
func TestAppendTextOwnsTheTextItKeeps(t *testing.T) {
	tracker := Track(appendTextDocument())
	big := []byte(strings.Repeat("x", 1<<20))
	got := appendTextCommit(t, tracker, func(root *Object) {
		//nolint:gosec // G103: the test cuts the text from a larger buffer on purpose, to prove AppendText copies it
		mustAppend(t, root.Array("content").Object(0), "text", unsafe.String(&big[0], 40))
	})
	copy(big, strings.Repeat("y", 40))
	if want := strings.Repeat("x", 40); got[0][2] != want {
		t.Fatalf("the op text changed with the buffer it was cut from: %q", got[0][2])
	}
}
