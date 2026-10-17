package turn

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func parseArray(t testing.TB, text string) []any {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	a, _ := v.([]any)
	return a
}

func objects(list []any) []*jv.Object {
	out := make([]*jv.Object, 0, len(list))
	for _, v := range list {
		out = append(out, v.(*jv.Object))
	}
	return out
}

func sectionsOf(pairs []any) *Sections {
	s := &Sections{}
	for _, p := range pairs {
		kv := p.([]any)
		s.Set(kv[0].(string), kv[1].(string))
	}
	return s
}

// TestPromptOracle replays src/harness/prompt.ts (pi-durable main da866ada, run by oracle/gen_prompt.mjs): replaySections
// and planSystemEntries over random system-message histories, head markers, section sets and tool sets.
func TestPromptOracle(t *testing.T) {
	var replays, plans, empty, multi, baselines, toolOnly int
	for n, f := range corpusLines(t, "prompt.hex") {
		switch f[0] {
		case "R":
			shown := ReplaySections(objects(parseArray(t, unhex(t, f[1]))))
			pairs := make([]any, 0, len(shown.Keys()))
			for _, k := range shown.Keys() {
				v, _ := shown.Get(k)
				pairs = append(pairs, []any{k, v})
			}
			if got, want := jv.Stringify(pairs), unhex(t, f[2]); got != want {
				t.Fatalf("line %d replaySections:\n got %s\nwant %s", n, got, want)
			}
			replays++
		case "P":
			in := parseObject(t, unhex(t, f[1]))
			view := PromptView{}
			if head, ok := in.Get("head"); ok && head != nil {
				id, _ := head.(*jv.Object).Get("id")
				view.HasHead, view.HeadID = true, int64(id.(float64))
			}
			entries, _ := in.Get("entries")
			for _, e := range entries.([]any) {
				eo := e.(*jv.Object)
				id, _ := eo.Get("id")
				kind, _ := eo.Get("kind")
				view.Entries = append(view.Entries, PromptEntry{ID: int64(id.(float64)), Kind: kind.(string)})
			}
			messages, _ := in.Get("messages")
			view.Messages = objects(messages.([]any))
			desired, _ := in.Get("desired")
			tools, _ := in.Get("tools")
			ts, _ := in.Get("timestamp")
			drafts := PlanSystemEntries(view, sectionsOf(desired.([]any)), objects(tools.([]any)), ts.(float64))
			list := make([]any, 0, len(drafts))
			for _, d := range drafts {
				list = append(list, d)
			}
			got, want := jv.Stringify(list), unhex(t, f[2])
			if got != want {
				t.Fatalf("line %d planSystemEntries:\n got %s\nwant %s", n, got, want)
			}
			plans++
			switch {
			case len(drafts) == 0:
				empty++
			case len(drafts) > 1:
				multi++
			}
			if view.HasHead && len(drafts) == 1 && len(want) > 0 {
				baselines++
			}
			if len(drafts) == 1 && len(want) > 0 && !view.HasHead {
				toolOnly++
			}
		}
	}
	if replays < 200 || plans < 200 || empty < 5 || multi < 5 || baselines < 5 {
		t.Fatalf("corpus covers replays=%d plans=%d empty=%d multi=%d baselines=%d toolOnly=%d", replays, plans, empty, multi, baselines, toolOnly)
	}
}
