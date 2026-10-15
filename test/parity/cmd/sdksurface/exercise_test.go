package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSDKConsistencyMatrixIsCurrent regenerates docs/extension-sdk-consistency.md
// and fails on drift, on a hole the ledger does not list, and on a ledger entry
// whose hole has closed.
func TestSDKConsistencyMatrixIsCurrent(t *testing.T) {
	m, err := buildMatrix(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	corpora, err := loadCorpora(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	gaps, err := loadGaps(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	rows := m.consistencyRows(corpora)
	want, err := os.ReadFile(filepath.Join(repoRoot, consistencyPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(renderConsistency(rows, gaps), want) {
		t.Errorf("%s is stale: run `go run ./test/parity/cmd/sdksurface`", consistencyPath)
	}
	if err := validateGaps(rows, gaps); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGapsRatchets(t *testing.T) {
	rows := []consistencyRow{
		{Key: "ctx.a", Present: map[string]bool{"go": true, "rust": true}, Exercised: map[string]bool{"go": true}},
		{Key: "ctx.b", Present: map[string]bool{"go": true}, Exercised: map[string]bool{"go": true}},
	}
	ok := []gapSpec{{Key: "ctx.a", SDKs: []string{"rust"}, Owner: "o", Row: "r", Reason: "why"}}
	if err := validateGaps(rows, ok); err != nil {
		t.Fatalf("a listed hole must pass: %v", err)
	}
	for name, test := range map[string]struct {
		gaps []gapSpec
		want string
	}{
		"unlisted hole": {nil, "ctx.a: no Rust conformance fixture reaches it"},
		"closed hole":   {[]gapSpec{{Key: "ctx.b", SDKs: []string{"go"}, Owner: "o", Row: "r", Reason: "why"}, ok[0]}, `gap "ctx.b" for Go is stale`},
		"unknown key":   {[]gapSpec{{Key: "ctx.z", SDKs: []string{"go"}, Owner: "o", Row: "r", Reason: "why"}, ok[0]}, `names no member or event row`},
		"no owner":      {[]gapSpec{{Key: "ctx.a", SDKs: []string{"rust"}, Row: "r", Reason: "why"}}, "needs owner, row and reason"},
	} {
		err := validateGaps(rows, test.gaps)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: got %v, want %q", name, err, test.want)
		}
	}
}

// A property the runtime reads is handed over as an object-literal key (`renderDiff(text, { filePath })`), so the key reaches a read: cell as a member access does; a name that is only a substring, a call or a ternary branch does not, and a generic member name keeps its receiver rule.
func TestExerciseTokensReadPropertyKey(t *testing.T) {
	member := matrixRow{surfaceRow: surfaceRow{Kind: kindMember}}
	reaches := func(sym, text string) bool {
		for _, re := range exerciseTokens(member, cell{Symbol: sym}) {
			if re.MatchString(text) {
				return true
			}
		}
		return false
	}
	for _, text := range []string{`render(x, { filePath: "a.go" })`, `f({a: 1, filePath: 2})`, `const o = {filePath : p}`, `o.filePath`} {
		if !reaches("read:diff.filePath", text) {
			t.Errorf("read:diff.filePath must be reached by %q", text)
		}
	}
	for _, text := range []string{`myfilePath: 1`, `x ? filePath : y`, `filePath(1)`, `"filePath"`} {
		if reaches("read:diff.filePath", text) {
			t.Errorf("read:diff.filePath must not be reached by %q", text)
		}
	}
	if reaches("Context.SetTitle", `{ SetTitle: 1 }`) {
		t.Error("a non-read cell is reached only by a member access")
	}
	if reaches("read:ctx.abort", `{ abort: 1 }`) {
		t.Error("a generic member name keeps its receiver rule even as a read key")
	}
}

func TestExerciseTokens(t *testing.T) {
	member := matrixRow{surfaceRow: surfaceRow{Kind: kindMember}}
	for sym, text := range map[string]string{"Context.SetTitle": "ctx.SetTitle(x)", "Context::set_title": "ctx.set_title(x)", "api.events.emit": "pi.events.emit(1)", "read:tool.promptSnippet": "tool.promptSnippet"} {
		hit := false
		for _, re := range exerciseTokens(member, cell{Symbol: sym}) {
			hit = hit || re.MatchString(text)
		}
		if !hit {
			t.Errorf("%s must match %q", sym, text)
		}
	}
	for _, re := range exerciseTokens(member, cell{Symbol: "Context.SetTitle"}) {
		if re.MatchString("SetTitleNow(x) // not a member access of SetTitle") {
			t.Errorf("%s matched an unrelated identifier", re)
		}
	}
	for _, re := range exerciseTokens(member, cell{Symbol: "RuntimeContext.abort"}) {
		if re.MatchString("controller.abort(); thread.abort()") {
			t.Errorf("%s matched an abort of an unrelated object", re)
		}
		if !re.MatchString("ctx.abort()") || !re.MatchString("pi.session_manager().abort()") {
			t.Errorf("%s must match a context abort", re)
		}
	}
	// An implemented member is reached by the host, so a fixture that defines it exercises it; a fixture that only names it does not.
	implemented := matrixRow{surfaceRow: surfaceRow{Kind: kindPig, Implemented: true}}
	for sym, def := range map[string]string{"ViewEventHandler.HandleViewEvent": "func (p *probe) HandleViewEvent(event kit.Event) {", "ViewComponent::handle_view_event": "    fn handle_view_event(&mut self, event: Event) {", "kit.ViewEventHandler.handle_view_event": "    def handle_view_event(self, event):"} {
		hit, hitOther := false, false
		for _, re := range exerciseTokens(implemented, cell{Symbol: sym}) {
			hit = hit || re.MatchString(def)
			hitOther = hitOther || re.MatchString("// HandleViewEvent handle_view_event is mentioned here")
		}
		if !hit || hitOther {
			t.Errorf("%s: definition matched=%v, a comment matched=%v; want true, false", sym, hit, hitOther)
		}
	}
	event := matrixRow{surfaceRow: surfaceRow{Kind: kindEvent, Event: "turn_start"}}
	hit := false
	for _, re := range exerciseTokens(event, cell{Symbol: "api.on"}) {
		hit = hit || re.MatchString(`pi.on("turn_start", h)`)
		if re.MatchString(`pi.on("agent_start", h)`) {
			t.Errorf("%s matched another event", re)
		}
	}
	if !hit {
		t.Error("an event handler registration by name must match")
	}
}

// TestNonGoCorpusTakesOnlyEmbeddedSources pins that a Go test file contributes to a non-Go runtime's corpus only through the extension sources it embeds as raw string literals: its comments, Go code and other strings name Go types and host actions, and counting them reported surfaces as reached that no fixture of that runtime touches.
func TestNonGoCorpusTakesOnlyEmbeddedSources(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test/extension-conformance")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	test := "package x\n\n// a tui.Image escape\nvar exempt = map[string]string{\"ui.getEditorComponent\": \"host-side\"}\n\nfunc f(ctx C) { _ = ctx.SessionManager() }\n\nconst src = `pi.registerTool({ name: \"probe\" })`\n"
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(test), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sdk := range []string{"node", "rust", "python"} {
		corpus, err := loadCorpus(root, sdk)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(corpus, "pi.registerTool") {
			t.Errorf("%s corpus lost the embedded source: %q", sdk, corpus)
		}
		for _, outside := range []string{"tui.Image", "ui.getEditorComponent", "SessionManager"} {
			if strings.Contains(corpus, outside) {
				t.Errorf("%s corpus took %q from outside a raw string literal", sdk, outside)
			}
		}
	}
	corpus, err := loadCorpus(root, "go")
	if err != nil {
		t.Fatal(err)
	}
	if corpus != "" {
		t.Errorf("a Go file that does not import the Go SDK joined the Go corpus: %q", corpus)
	}
}
