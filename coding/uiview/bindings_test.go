package uiview

import (
	"encoding/json"
	"strings"
	"testing"
)

// bindingRecord is a panel mount whose content declares four intents and the given bindings: fx.tap takes no member,
// fx.view a required value of day, week or month, fx.note a string, and fx.pick belongs to the node n-base.
func bindingRecord(bindings ...any) map[string]any {
	m := mountRecord("k", map[string]any{"bindings": bindings})
	m["intents"] = []any{
		map[string]any{"name": "fx.tap", "arg": map[string]any{"type": "object"}},
		map[string]any{"name": "fx.view", "arg": map[string]any{"type": "object", "required": []any{"value"},
			"properties": map[string]any{"value": map[string]any{"type": "enum", "enum": []any{"day", "week", "month"}}}}},
		map[string]any{"name": "fx.note", "arg": map[string]any{"type": "object", "properties": map[string]any{"s": map[string]any{"type": "string"}}}},
		map[string]any{"name": "fx.pick", "nodeKey": "n-base", "arg": map[string]any{"type": "object"}},
	}
	return m
}

func keyBinding(key, intent string) map[string]any {
	return map[string]any{"key": key, "intent": intent}
}

func viewBinding(key, value string) map[string]any {
	return map[string]any{"key": key, "intent": "fx.view", "arg": map[string]any{"value": value}}
}

// rawMember returns one member of a snapshot as the client is sent it, or nil when the snapshot has none.
func rawMember(t *testing.T, s Snapshot, name string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	return all[name]
}

// wideAbilities takes a constant argument of the largest size a binding may carry.
func wideAbilities() ClientAbilities {
	a := richAbilities()
	a.Limits.IntentArgBytes = 4096
	return a
}

func TestBindingsAccepted(t *testing.T) {
	a := richAbilities()
	// The calendar's keys: three keys raise one intent with different arguments, one raises an intent without one, and
	// a key and a gesture page.
	wantOK(t, "calendar keys", tryMount(t, a, bindingRecord(
		viewBinding("d", "day"), viewBinding("w", "week"), viewBinding("m", "month"), keyBinding("t", "fx.tap"),
		keyBinding("right", "fx.tap"), map[string]any{"gesture": "swipe-left", "intent": "fx.tap", "name": "Next"},
		keyBinding("left", "fx.tap"), map[string]any{"gesture": "swipe-right", "intent": "fx.tap"},
		map[string]any{"gesture": "swipe-up", "intent": "fx.tap"}, map[string]any{"gesture": "swipe-down", "intent": "fx.tap"})))
	for _, key := range []string{"a", "z", "0", "9", "enter", "space", "backspace", "delete", "insert", "home", "end", "pageUp", "pageDown", "up", "down",
		"f1", "f5", "f7", "f12", "?", "/", "[", "\\", "`", "shift+left", "shift+a", "ctrl+p", "alt+enter", "ctrl+shift+p", "alt+ctrl+shift+f5", "ctrl+1", "alt+/",
		"c", "shift+c", "alt+c", "d", "ctrl+x", "+", "ctrl++", "alt+ctrl++"} {
		wantOK(t, key, tryMount(t, a, bindingRecord(keyBinding(key, "fx.tap"))))
	}
}

func TestBindingsBounds(t *testing.T) {
	a := wideAbilities()
	many := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = keyBinding(string("abefghijklmnopqrstuvwxy0123456789"[i]), "fx.tap")
		}
		return out
	}
	wantOK(t, "32 bindings", tryMount(t, a, bindingRecord(many(32)...)))
	wantCode(t, tryMount(t, a, bindingRecord(many(33)...)), CodeInvalid)

	arg := func(n int) map[string]any {
		// {"s":"..."} is 8 bytes around the string.
		return map[string]any{"key": "a", "intent": "fx.note", "arg": map[string]any{"s": strings.Repeat("s", n-8)}}
	}
	wantOK(t, "1024-byte arg", tryMount(t, a, bindingRecord(arg(1024))))
	wantCode(t, tryMount(t, a, bindingRecord(arg(1025))), CodeInvalid)
	// The constant argument is raised as it is, so it must fit the client's intentArgBytes like any other argument.
	wantCode(t, tryMount(t, richAbilities(), bindingRecord(arg(richAbilities().Limits.IntentArgBytes+1))), CodeLimit)
	wantOK(t, "arg of the client's size", tryMount(t, richAbilities(), bindingRecord(arg(richAbilities().Limits.IntentArgBytes))))

	name := func(n int) map[string]any {
		return map[string]any{"key": "a", "intent": "fx.tap", "name": strings.Repeat("n", n)}
	}
	wantOK(t, "256-byte name", tryMount(t, a, bindingRecord(name(256))))
	wantCode(t, tryMount(t, a, bindingRecord(name(257))), CodeInvalid)
}

func TestBindingsRefused(t *testing.T) {
	a := richAbilities()
	for what, bindings := range map[string][]any{
		"no trigger":             {map[string]any{"intent": "fx.tap"}},
		"two triggers":           {map[string]any{"key": "a", "gesture": "swipe-left", "intent": "fx.tap"}},
		"no intent":              {map[string]any{"key": "a"}},
		"unknown key":            {keyBinding("hyper", "fx.tap")},
		"alias esc":              {keyBinding("esc", "fx.tap")},
		"alias return":           {keyBinding("return", "fx.tap")},
		"upper-case letter":      {keyBinding("A", "fx.tap")},
		"upper-case modifier":    {keyBinding("Ctrl+a", "fx.tap")},
		"lower-case pageup":      {keyBinding("pageup", "fx.tap")},
		"unknown modifier":       {keyBinding("super+a", "fx.tap")},
		"modifier twice":         {keyBinding("ctrl+ctrl+a", "fx.tap")},
		"modifier alone":         {keyBinding("ctrl+", "fx.tap")},
		"shift and plus":         {keyBinding("shift++", "fx.tap")},
		"plus twice":             {keyBinding("++", "fx.tap")},
		"shifted digit":          {keyBinding("shift+1", "fx.tap")},
		"shifted symbol":         {keyBinding("shift+/", "fx.tap")},
		"two keys":               {keyBinding("a+b", "fx.tap")},
		"unknown gesture":        {map[string]any{"gesture": "pinch", "intent": "fx.tap"}},
		"key twice":              {viewBinding("d", "day"), viewBinding("d", "week")},
		"key twice, other order": {keyBinding("ctrl+shift+p", "fx.tap"), keyBinding("shift+ctrl+p", "fx.tap")},
		"gesture twice":          {map[string]any{"gesture": "swipe-left", "intent": "fx.tap"}, map[string]any{"gesture": "swipe-left", "intent": "fx.tap"}},
		"reserved escape":        {keyBinding("escape", "fx.tap")},
		"reserved ctrl+escape":   {keyBinding("ctrl+escape", "fx.tap")},
		"reserved tab":           {keyBinding("tab", "fx.tap")},
		"reserved shift+tab":     {keyBinding("shift+tab", "fx.tap")},
		"reserved f6":            {keyBinding("f6", "fx.tap")},
		"reserved alt+f6":        {keyBinding("alt+f6", "fx.tap")},
		"reserved ctrl+c":        {keyBinding("ctrl+c", "fx.tap")},
		"reserved ctrl+shift+c":  {keyBinding("ctrl+shift+c", "fx.tap")},
		"reserved shift+ctrl+c":  {keyBinding("shift+ctrl+c", "fx.tap")},
		"reserved ctrl+d":        {keyBinding("ctrl+d", "fx.tap")},
		"reserved alt+ctrl+z":    {keyBinding("alt+ctrl+z", "fx.tap")},
		"undeclared intent":      {keyBinding("a", "fx.none")},
		"intent of a node":       {keyBinding("a", "fx.pick")},
		"arg outside the shape":  {viewBinding("a", "year")},
		"no arg, one required":   {keyBinding("a", "fx.view")},
		"null arg":               {map[string]any{"key": "a", "intent": "fx.tap", "arg": nil}},
		"unknown member":         {map[string]any{"key": "a", "intent": "fx.tap", "when": "focused"}},
		"not a list":             nil,
	} {
		m := bindingRecord(bindings...)
		if bindings == nil {
			m["bindings"] = map[string]any{"key": "a", "intent": "fx.tap"}
		}
		if err := tryMount(t, a, m); err == nil || err.Code != CodeInvalid {
			t.Errorf("%s: error = %v, want invalid", what, err)
		}
	}
}

func TestBindingsAreDeliveredWholeAndDigested(t *testing.T) {
	r := New(Options{})
	if _, _, err := r.Attach(richAbilities(), nil); err != nil {
		t.Fatal(err)
	}
	sent := []any{viewBinding("d", "day"), viewBinding("shift+ctrl+w", "week"), map[string]any{"gesture": "swipe-left", "intent": "fx.tap", "name": "Next"}}
	rec := bindingRecord(sent...)
	rec["fallback"] = map[string]any{"kind": "text", "text": "fb"}
	args, err := decodeRecord(t, rec)
	wantOK(t, "decode", err)
	st, err := r.Mount("p", args)
	wantOK(t, "mount", err)

	// A client is sent every binding as the plugin wrote it, the spelling of a key included, whatever input it states.
	want := `[{"arg":{"value":"day"},"intent":"fx.view","key":"d"},{"arg":{"value":"week"},"intent":"fx.view","key":"shift+ctrl+w"},{"gesture":"swipe-left","intent":"fx.tap","name":"Next"}]`
	keysOnly := richAbilities()
	keysOnly.Input.Pointer = false
	var digest string
	for what, a := range map[string]ClientAbilities{"pointer client": richAbilities(), "keyboard client": keysOnly} {
		s := resolved(t, r, a)
		got, cerr := Canonical(rawMember(t, s, "bindings"))
		if cerr != nil || string(got) != want {
			t.Errorf("%s: bindings = %s (%v), want %s", what, got, cerr, want)
		}
		if digest == "" {
			digest = s.Digest
		}
		if len(s.Digest) != 64 || s.Digest != digest {
			t.Errorf("%s: digest %q, want the one digest %q", what, s.Digest, digest)
		}
	}

	// A client that is sent the fallback is sent no binding: what the keys act on is not on its screen.
	small := richAbilities()
	small.Limits.Nodes = 0
	if s := resolved(t, r, small); !s.Degraded || rawMember(t, s, "bindings") != nil {
		t.Errorf("degraded client: degraded %v bindings %s", s.Degraded, rawMember(t, s, "bindings"))
	}

	// An update replaces the bindings and the digest follows; a refused update leaves them as they were.
	update := func(bindings ...any) (Status, *Error) {
		u := bindingRecord(bindings...)
		delete(u, "key")
		delete(u, "slot")
		u["mount"] = map[string]any{"key": "k", "epoch": st.Mount.Epoch, "rev": st.Mount.Rev}
		raw, merr := json.Marshal(u)
		if merr != nil {
			t.Fatal(merr)
		}
		ua, derr := DecodeUpdate(raw)
		if derr != nil {
			return Status{}, derr
		}
		return r.Update("p", ua)
	}
	_, err = update(keyBinding("escape", "fx.tap"))
	wantCode(t, err, CodeInvalid)
	if s := resolved(t, r, richAbilities()); s.Digest != digest || s.Mount.Rev != st.Mount.Rev {
		t.Errorf("after a refused update: digest %q rev %d", s.Digest, s.Mount.Rev)
	}
	st, err = update(viewBinding("d", "month"))
	wantOK(t, "update", err)
	s := resolved(t, r, richAbilities())
	if got, _ := Canonical(rawMember(t, s, "bindings")); string(got) != `[{"arg":{"value":"month"},"intent":"fx.view","key":"d"}]` || s.Digest == digest || len(s.Digest) != 64 {
		t.Errorf("after the update: bindings %s digest %q (was %q)", got, s.Digest, digest)
	}

	// A client that cannot honour a binding says so through its report; the plugin reads which one.
	att, _, err := r.Attach(keysOnly, nil)
	wantOK(t, "attach", err)
	defer att.Detach()
	wantOK(t, "report", att.Report("p", st.Mount, Notice{Code: string(CodeUnsupported), Severity: SeverityInfo, Path: "bindings[0]", Message: "no key reaches this client"}))
	diag, err := r.Diagnostics("p", StateArgs{Key: "k"})
	wantOK(t, "diagnostics", err)
	last := diag.Notices[len(diag.Notices)-1]
	if last.Code != "unsupported" || last.Severity != SeverityInfo || last.Path != "bindings[0]" || last.Count != 1 {
		t.Errorf("the client's report reads %+v", last)
	}
}
