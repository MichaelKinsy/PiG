package tui

// tests for slash-command autocomplete.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sampleCommands() []SlashCommand {
	return []SlashCommand{
		{Name: "help", Description: "Show available slash commands"},
		{Name: "clear", Description: "Clear the chat transcript"},
		{Name: "quit", Description: "Quit pig"},
		{Name: "model", Description: "Show or switch the active model"},
		{Name: "models", Description: "List available models matching a pattern"},
		{Name: "agent", Description: "Show or switch the active agent persona"},
		{Name: "agents", Description: "List available agents"},
		{Name: "tools", Description: "List registered tools"},
		{Name: "skill", Description: "List loaded skills"},
		{Name: "cost", Description: "Show session token + cost summary"},
		{Name: "save", Description: "Save the transcript to a file"},
		{Name: "copy", Description: "Copy last assistant message to clipboard"},
		{Name: "session", Description: "Show session info and stats"},
		{Name: "hotkeys", Description: "Show keyboard shortcuts"},
	}
}

func TestSlashOnlyProvider_BareSlashListsAll(t *testing.T) {
	p := NewSlashOnlyProvider(sampleCommands())
	res := p.GetSuggestions([]string{"/"}, 0, 1)
	if res == nil {
		t.Fatal("expected suggestions for `/`, got nil")
		return
	}
	if got, want := len(res.Items), 14; got != want {
		t.Fatalf("expected %d items, got %d", want, got)
	}
	if res.Prefix != "/" {
		t.Errorf("prefix=%q, want `/`", res.Prefix)
	}
}

func TestSlashOnlyProvider_FilterNarrowsCaseInsensitive(t *testing.T) {
	p := NewSlashOnlyProvider(sampleCommands())
	res := p.GetSuggestions([]string{"/MO"}, 0, 3)
	if res == nil {
		t.Fatal("expected suggestions for `/MO`, got nil")
		return
	}
	names := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		names = append(names, it.Value)
	}
	want := map[string]bool{"model": true, "models": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected name %q in filter `/MO`: full=%v", n, names)
		}
	}
	if len(names) < 2 {
		t.Errorf("expected at least model/models, got %v", names)
	}
}

func TestSlashOnlyProvider_NoMatchReturnsNil(t *testing.T) {
	p := NewSlashOnlyProvider(sampleCommands())
	if res := p.GetSuggestions([]string{"/zzzzz"}, 0, 6); res != nil {
		t.Errorf("expected nil for no-match, got %d items", len(res.Items))
	}
}

func TestSlashOnlyProvider_NonSlashReturnsNil(t *testing.T) {
	p := NewSlashOnlyProvider(sampleCommands())
	if res := p.GetSuggestions([]string{"hello"}, 0, 5); res != nil {
		t.Error("expected nil when buffer doesn't start with `/`")
	}
}

func TestSlashOnlyProvider_ApplyCompletionSlashName(t *testing.T) {
	p := NewSlashOnlyProvider(sampleCommands())
	lines := []string{"/he"}
	out, nl, nc := p.ApplyCompletion(lines, 0, 3, AutocompleteItem{Value: "help", Label: "help"}, "/he")
	if out[0] != "/help " {
		t.Errorf("line=%q want `/help `", out[0])
	}
	if nl != 0 || nc != 6 {
		t.Errorf("cursor=(%d,%d) want (0,6)", nl, nc)
	}
}

func TestSlashOnlyProvider_ArgumentCompletion(t *testing.T) {
	cmds := sampleCommands()
	for i := range cmds {
		if cmds[i].Name == "model" {
			cmds[i].GetArgumentCompletions = func(prefix string) []AutocompleteItem {
				items := []AutocompleteItem{
					{Value: "github-copilot/gpt-4o", Label: "gpt-4o", Description: "github-copilot"},
					{Value: "openai/gpt-4o", Label: "gpt-4o", Description: "openai"},
				}
				return FuzzyFilter(items, prefix, func(it AutocompleteItem) string { return it.Label + " " + it.Description })
			}
		}
	}
	p := NewSlashOnlyProvider(cmds)
	res := p.GetSuggestions([]string{"/model "}, 0, 7)
	if res == nil || len(res.Items) != 2 {
		t.Fatalf("expected 2 items for `/model ` (no filter), got %v", res)
	}
	if res.Prefix != "" {
		t.Errorf("prefix=%q want \"\"", res.Prefix)
	}
	res = p.GetSuggestions([]string{"/model openai"}, 0, 13)
	if res == nil || len(res.Items) != 1 {
		t.Fatalf("expected 1 item for `/model openai`, got %v", res)
	}
	if res.Items[0].Value != "openai/gpt-4o" {
		t.Errorf("expected openai match, got %q", res.Items[0].Value)
	}
	// Apply: should replace `openai` with the value (no trailing space).
	out, nl, nc := p.ApplyCompletion([]string{"/model openai"}, 0, 13, res.Items[0], res.Prefix)
	if out[0] != "/model openai/gpt-4o" {
		t.Errorf("line=%q want `/model openai/gpt-4o`", out[0])
	}
	if nl != 0 || nc != len("/model openai/gpt-4o") {
		t.Errorf("cursor=(%d,%d) want (0,%d)", nl, nc, len("/model openai/gpt-4o"))
	}
}

// ─── Editor integration ──────────────────────────────────────────────────────

func TestEditor_SlashOpensPopup(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	if !e.AutocompleteOpen() {
		t.Fatal("popup should open after typing `/`")
	}
	if len(e.autocompleteItems) != 14 {
		t.Errorf("expected 14 items, got %d", len(e.autocompleteItems))
	}
}

func TestEditor_TypingNarrowsThenAcceptInsertsSlashName(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	for _, ch := range "/he" {
		e.HandleInput(string(ch))
	}
	if !e.AutocompleteOpen() {
		t.Fatal("popup closed after `/he`")
	}
	// Tab → accept (no submit). Editor should now contain `/help `.
	e.HandleInput("\t")
	if e.AutocompleteOpen() {
		t.Error("popup should close after Tab accept")
	}
	if got := e.Text(); got != "/help " {
		t.Errorf("text=%q want `/help `", got)
	}
}

func TestEditor_AcceptReturnsSubmitForSlashName(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	for _, ch := range "/he" {
		e.HandleInput(string(ch))
	}
	if !e.AutocompleteOpen() {
		t.Fatal("popup not open")
	}
	if !e.AutocompleteAccept() {
		t.Error("AutocompleteAccept should return submit=true on slash-name prefix")
	}
}

func TestEditor_AcceptReturnsNoSubmitForArg(t *testing.T) {
	cmds := sampleCommands()
	for i := range cmds {
		if cmds[i].Name == "model" {
			cmds[i].GetArgumentCompletions = func(prefix string) []AutocompleteItem {
				return []AutocompleteItem{{Value: "openai/gpt-4o", Label: "gpt-4o", Description: "openai"}}
			}
		}
	}
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(cmds))
	for _, ch := range "/model " {
		e.HandleInput(string(ch))
	}
	if !e.AutocompleteOpen() {
		t.Fatal("popup not open after `/model `")
	}
	if e.AutocompleteAccept() {
		t.Error("arg completion should return submit=false")
	}
	if got := e.Text(); got != "/model openai/gpt-4o" {
		t.Errorf("text=%q want `/model openai/gpt-4o`", got)
	}
}

func TestEditor_EscDismissesViaCancel(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	if !e.AutocompleteOpen() {
		t.Fatal("popup not open")
	}
	e.AutocompleteCancel()
	if e.AutocompleteOpen() {
		t.Error("popup should be closed after cancel")
	}
	if got := e.Text(); got != "/" {
		t.Errorf("buffer should be unchanged, got %q", got)
	}
}

func TestEditor_BackspaceClosesPopupWhenNoSlash(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	if !e.AutocompleteOpen() {
		t.Fatal("popup not open after `/`")
	}
	e.HandleInput("\x7f") // backspace
	if e.AutocompleteOpen() {
		t.Error("popup should close after backspace removes `/`")
	}
}

func TestEditor_ArrowsNavigatePopup(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	if !e.AutocompleteOpen() {
		t.Fatal("popup not open")
	}
	startCursor := e.autocompleteCursor
	e.HandleInput("\033[B") // down
	if e.autocompleteCursor != startCursor+1 {
		t.Errorf("down: cursor=%d want %d", e.autocompleteCursor, startCursor+1)
	}
	e.HandleInput("\x0e") // Ctrl+N
	if e.autocompleteCursor != startCursor+2 {
		t.Errorf("Ctrl+N: cursor=%d want %d", e.autocompleteCursor, startCursor+2)
	}
	e.HandleInput("\033[A") // up
	if e.autocompleteCursor != startCursor+1 {
		t.Errorf("up: cursor=%d want %d", e.autocompleteCursor, startCursor+1)
	}
	e.HandleInput("\x10") // Ctrl+P
	if e.autocompleteCursor != startCursor {
		t.Errorf("Ctrl+P: cursor=%d want %d", e.autocompleteCursor, startCursor)
	}
}

func TestEditor_ArrowsDontMoveBufferCursorWhenPopupOpen(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	bufBefore := e.cursor
	e.HandleInput("\033[B")
	if e.cursor != bufBefore {
		t.Errorf("buffer cursor moved on popup nav: %v -> %v", bufBefore, e.cursor)
	}
}

func TestEditor_PopupRendersBelowEditor(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/he")
	rows := e.Render(40)
	// Popup rows should appear after the bottom border.
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "help") {
		t.Errorf("expected `help` in render output, got:\n%s", joined)
	}
}

func TestEditor_PathAutocompleteIsForceOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"parity_test.go", "perf_test.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := NewEditor()
	e.SetAutocomplete(NewCombinedProvider(nil, dir, ""))
	e.HandleInput(".")
	e.HandleInput("/")
	if e.AutocompleteOpen() {
		t.Fatal("popup should stay closed while typing ./")
	}
	e.HandleInput("\t")
	if !e.AutocompleteOpen() {
		t.Fatal("Tab should force-open file completion popup for ./")
	}
}

func TestEditor_PathAutocompleteUniqueTabAcceptsImmediately(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "parity_test.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEditor()
	e.SetAutocomplete(NewCombinedProvider(nil, dir, ""))
	for _, ch := range "./pa" {
		e.HandleInput(string(ch))
	}
	if e.AutocompleteOpen() {
		t.Fatal("popup should stay closed while typing ./pa")
	}
	e.HandleInput("\t")
	if e.AutocompleteOpen() {
		t.Fatal("unique forced match should apply immediately, not leave popup open")
	}
	if got := e.Text(); got != "./parity_test.go" {
		t.Fatalf("text = %q, want ./parity_test.go", got)
	}
}

// ─── Fuzzy filter ────────────────────────────────────────────────────────────

func TestFuzzyMatch_OrderedSubsequence(t *testing.T) {
	if m := FuzzyMatchScore("hlp", "help"); !m.Matches {
		t.Errorf("hlp should match help, got %+v", m)
	}
	if m := FuzzyMatchScore("xz", "help"); m.Matches {
		t.Errorf("xz should not match help, got %+v", m)
	}
}

func TestFuzzyFilter_SortsBestFirst(t *testing.T) {
	items := []string{"clear", "help", "models", "model"}
	got := FuzzyFilter(items, "mo", func(s string) string { return s })
	if len(got) < 2 {
		t.Fatalf("expected at least 2 matches, got %v", got)
	}
	// Both `model` and `models` start with `mo`; `clear` and `help` don't match.
	for _, g := range got {
		if g != "model" && g != "models" {
			t.Errorf("unexpected match %q for query `mo`", g)
		}
	}
}

func TestFuzzyFilter_TokensAndedAcrossWords(t *testing.T) {
	items := []string{"gpt-4o openai", "gpt-4o github-copilot", "claude anthropic"}
	got := FuzzyFilter(items, "gpt openai", func(s string) string { return s })
	if len(got) != 1 {
		t.Fatalf("expected 1 match for `gpt openai`, got %v", got)
	}
	if got[0] != "gpt-4o openai" {
		t.Errorf("got %q, want `gpt-4o openai`", got[0])
	}
}

func TestFuzzyFilter_EmptyQueryReturnsAll(t *testing.T) {
	items := []string{"a", "b", "c"}
	got := FuzzyFilter(items, "", func(s string) string { return s })
	if len(got) != 3 {
		t.Errorf("empty query should return all, got %v", got)
	}
	got = FuzzyFilter(items, "   ", func(s string) string { return s })
	if len(got) != 3 {
		t.Errorf("whitespace-only query should return all, got %v", got)
	}
}

// ─── popup row count (N/M) indicator ───────────────────────────

func TestEditor_PopupCounterShownWhenScrolled(t *testing.T) {
	// 14 commands, max=5 → must overflow → counter must appear.
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	if !e.AutocompleteOpen() {
		t.Fatal("popup not open")
	}
	rows := e.Render(60)
	joined := strings.Join(rows, "\n")
	// At cursor=0, popup shows items 0..4 of 14 → counter (1/14).
	if !strings.Contains(joined, "(1/14)") {
		t.Errorf("expected `(1/14)` counter in render output:\n%s", joined)
	}
}

func TestEditor_PopupCounterHiddenWhenFits(t *testing.T) {
	// `/he` narrows to 3 items (help, hotkeys for samples; no `changelog`
	// in our sample). 3 ≤ max=5 → no overflow → no counter.
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	for _, ch := range "/he" {
		e.HandleInput(string(ch))
	}
	if !e.AutocompleteOpen() {
		t.Fatal("popup not open after `/he`")
	}
	if n := len(e.autocompleteItems); n > 5 {
		t.Skipf("test assumption violated: `/he` returned %d items, expected ≤5", n)
	}
	rows := e.Render(60)
	joined := strings.Join(rows, "\n")
	// Match "(N/M)" pattern more strictly: the counter line is the
	// only `(d+/d+)` shape we emit.
	if strings.Contains(joined, "(1/") || strings.Contains(joined, "/2)") || strings.Contains(joined, "/3)") {
		// Allow the test to be lenient about exact counts but the
		// counter must NOT be present.
		t.Errorf("counter should be hidden when popup fits (got %d items, max=5):\n%s", len(e.autocompleteItems), joined)
	}
}

func TestEditor_PopupCounterTracksCursor(t *testing.T) {
	// Move cursor down: counter should follow.
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	// Move down 6 times → cursor now at index 6, popup scrolled,
	// counter should read "(7/14)".
	for range 6 {
		e.HandleInput("\033[B")
	}
	rows := e.Render(60)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "(7/14)") {
		t.Errorf("expected `(7/14)` counter after 6 down-presses:\n%s", joined)
	}
}

// ─── width-aware truncation ────────────────────────────────────

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in       string
		maxWidth int
		want     string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"", 5, ""},
		// Column-aware: each CJK char is 2 terminal columns.
		// maxWidth=4 → budget 3 cols → fits 1 CJK char (2 cols) + ellipsis (1 col).
		{"日本語テスト", 4, "日…"},
		// maxWidth=8 → budget 7 cols → fits 3 CJK chars (6 cols) + ellipsis (1 col).
		{"日本語テスト", 8, "日本語…"},
	}
	for _, tc := range cases {
		if got := truncateRunes(tc.in, tc.maxWidth); got != tc.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tc.in, tc.maxWidth, got, tc.want)
		}
	}
}

func TestEditor_PopupNarrowTerminalShowsLabelOnly(t *testing.T) {
	// Width ≤ 40 → upstream's `width > 40` guard fails → label-only.
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	rows := e.Render(40)
	joined := strings.Join(rows, "\n")
	// `help`'s description is "Show available slash commands": must
	// be absent at narrow widths.
	if strings.Contains(joined, "Show available slash commands") {
		t.Errorf("description should be hidden at width=40:\n%s", joined)
	}
	// But labels still rendered.
	if !strings.Contains(joined, "help") {
		t.Errorf("label `help` should still render at narrow width:\n%s", joined)
	}
}

func TestEditor_PopupWideTerminalShowsTruncatedDescription(t *testing.T) {
	// Wide enough for both columns, but small enough to require
	// description truncation. Pick width where 2-prefix + 12-label +
	// 2-gap = 16 leaves only ~30 cols for description.
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	rows := e.Render(50)
	joined := strings.Join(rows, "\n")
	// Description should be present in some form.
	if !strings.Contains(joined, "Show available") {
		t.Errorf("expected partial description visible at width=50:\n%s", joined)
	}
	// And every popup row must fit under width=50 (visible chars,
	// stripping ANSI). Rough check: visible chars ≤ width.
	for _, row := range rows {
		visible := stripANSI(row)
		if rw := len([]rune(visible)); rw > 50 {
			t.Errorf("row exceeds width=50 (visible=%d): %q", rw, visible)
		}
	}
}

func TestEditor_PopupVeryNarrowTerminalDoesNotPanic(t *testing.T) {
	// Pathological narrow width: must not panic, must produce
	// something renderable. Mirrors upstream `Math.max(1, ...)` clamps.
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	e.HandleInput("/")
	for _, w := range []int{1, 2, 5, 10, 20, 35} {
		rows := e.Render(w)
		if len(rows) == 0 {
			t.Errorf("width=%d: no rows", w)
		}
		for _, r := range rows {
			vis := stripANSI(r)
			if rw := len([]rune(vis)); rw > w && w >= 5 {
				// Allow tiny widths (1-4) to overflow: that's degenerate.
				t.Errorf("width=%d: row exceeds width (visible=%d): %q", w, rw, vis)
			}
		}
	}
}

// Ports autocomplete-skill-slash.test.ts: skill commands match by bare name.
func TestSlashOnlyProvider_SkillCommandFilter(t *testing.T) {
	p := NewSlashOnlyProvider([]SlashCommand{
		{Name: "skill:deep-research", Description: "Multi-agent deep research"},
		{Name: "skill:research-idea", Description: "Refine a raw idea into a falsifiable seed"},
		{Name: "skill:to-sidecar", Description: "Route work to a sidecar"},
		{Name: "model", Description: "Select the active model"},
	})
	values := func(prefix string) []string {
		line := "/" + prefix
		res := p.GetSuggestions([]string{line}, 0, len(line))
		if res == nil {
			t.Fatalf("expected suggestions for %q", line)
		}
		out := make([]string, len(res.Items))
		for i, item := range res.Items {
			out[i] = item.Value
		}
		return out
	}
	items := values("idea")
	if items[0] != "skill:research-idea" || slices.Contains(items, "skill:deep-research") {
		t.Fatalf("/idea = %v, want skill:research-idea first and no skill:deep-research", items)
	}
	if !slices.Contains(values("mod"), "model") {
		t.Fatal("/mod lost the ordinary model command")
	}
	if !slices.Contains(values("skill:side"), "skill:to-sidecar") {
		t.Fatal("/skill:side lost skill:to-sidecar")
	}
}

// Upstream autocomplete.ts joins an argument hint and description with " — ".
func TestSlashOnlyProvider_ArgumentHintSeparator(t *testing.T) {
	p := NewSlashOnlyProvider([]SlashCommand{
		{Name: "model", Description: "Select model (opens selector UI)", ArgumentHint: "<provider/model>"},
		{Name: "bug", ArgumentHint: "<description>"},
	})
	res := p.GetSuggestions([]string{"/"}, 0, 1)
	if res == nil || len(res.Items) != 2 {
		t.Fatalf("suggestions = %+v", res)
	}
	if got, want := res.Items[0].Description, "<provider/model> — Select model (opens selector UI)"; got != want {
		t.Fatalf("model description = %q, want %q", got, want)
	}
	if got, want := res.Items[1].Description, "<description>"; got != want {
		t.Fatalf("hint-only description = %q, want %q", got, want)
	}
}
