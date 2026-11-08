package runtime

import "testing"

func TestParseWhenAndEvaluate(t *testing.T) {
	parsed, err := ParseWhen("chat && editorEmpty && !overlay")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Specificity != 3 || len(parsed.Terms) != 3 || !parsed.Positive["chat"] || parsed.Positive["overlay"] {
		t.Fatalf("parsed = %+v", parsed)
	}
	if !EvaluateWhen(parsed.Expr, map[string]bool{"chat": true, "editorEmpty": true}) {
		t.Fatal("should hold")
	}
	if EvaluateWhen(parsed.Expr, map[string]bool{"chat": true, "editorEmpty": true, "overlay": true}) {
		t.Fatal("overlay negated")
	}
	either, err := ParseWhen("(sessions || chat) && !guest")
	if err != nil || !EvaluateWhen(either.Expr, map[string]bool{"chat": true}) || EvaluateWhen(either.Expr, map[string]bool{"chat": true, "guest": true}) {
		t.Fatalf("either: %v", err)
	}
	if blank, _ := ParseWhen("  "); blank.Specificity != 0 || !EvaluateWhen(blank.Expr, nil) {
		t.Fatal("blank is true")
	}
}

func TestParseWhenRejects(t *testing.T) {
	for _, source := range []string{"(chat", "chat &&", "&& chat", "chat sessions", "1x"} {
		if _, err := ParseWhen(source); err == nil {
			t.Errorf("%q parsed", source)
		}
	}
}

func TestCanOverlapExclusiveGroups(t *testing.T) {
	chat, _ := ParseWhen("chat")
	sessions, _ := ParseWhen("sessions")
	exclusive := [][]string{{"sessions", "chat"}}
	if CanOverlap(chat, sessions, exclusive) {
		t.Fatal("screens are exclusive")
	}
	if !CanOverlap(chat, sessions, nil) {
		t.Fatal("without groups they overlap")
	}
}
