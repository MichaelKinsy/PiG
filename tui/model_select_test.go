package tui

// pi: packages/coding-agent/src/modes/interactive/components/model-selector.ts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// stripANSI is internal to the tui package; reuse it.

func mkItems(specs ...string) []ModelSelectorItem {
	out := make([]ModelSelectorItem, 0, len(specs))
	for _, s := range specs {
		before, after, ok := strings.Cut(s, "/")
		if !ok {
			continue
		}
		out = append(out, ModelSelectorItem{Provider: before, ID: after})
	}
	return out
}

func TestModelSelector_DefaultsToScoped_WhenAuthExists(t *testing.T) {
	scoped := mkItems("github-copilot/gpt-4o", "openai/gpt-4o-mini")
	all := mkItems("github-copilot/gpt-4o", "openai/gpt-4o-mini", "openrouter/llama-3")
	ms := NewStaticModelSelectorComponent("Select model", scoped, all, "github-copilot/gpt-4o")
	if ms.Scope() != ModelScopeScoped {
		t.Fatalf("expected default scope=scoped, got %v", ms.Scope())
	}
	if ms.VisibleCount() != 2 {
		t.Errorf("expected 2 visible items in scoped, got %d", ms.VisibleCount())
	}
}

func TestModelSelector_DefaultsToAll_WhenNoAuth_WithWarning(t *testing.T) {
	all := mkItems("openai/gpt-4o", "groq/llama-3")
	ms := NewStaticModelSelectorComponent("Select model", nil, all, "")
	if ms.Scope() != ModelScopeAll {
		t.Fatalf("expected default scope=all when no auth, got %v", ms.Scope())
	}
	frame := ms.Render(80)
	joined := strings.Join(frame, "\n")
	if !strings.Contains(joined, "Only showing models from configured providers. Use /login to add providers.") {
		t.Errorf("expected no-auth warning in render, got:\n%s", joined)
	}
	// When noAuth, scope+hint lines should NOT appear (upstream behavior).
	if strings.Contains(joined, "Scope: all | scoped") {
		t.Errorf("scope header should not appear when noAuth, got:\n%s", joined)
	}
}

func TestModelSelector_TabCyclesScope(t *testing.T) {
	scoped := mkItems("github-copilot/gpt-4o")
	all := mkItems("github-copilot/gpt-4o", "openai/gpt-4o", "groq/llama-3")
	ms := NewStaticModelSelectorComponent("Select model", scoped, all, "github-copilot/gpt-4o")
	if ms.Scope() != ModelScopeScoped || ms.VisibleCount() != 1 {
		t.Fatalf("initial state wrong: scope=%v count=%d", ms.Scope(), ms.VisibleCount())
	}
	ms.HandleInput("\t")
	if ms.Scope() != ModelScopeAll || ms.VisibleCount() != 3 {
		t.Errorf("after Tab: expected scope=all count=3, got scope=%v count=%d", ms.Scope(), ms.VisibleCount())
	}
	ms.HandleInput("\t")
	if ms.Scope() != ModelScopeScoped || ms.VisibleCount() != 1 {
		t.Errorf("after Tab again: expected scope=scoped count=1, got scope=%v count=%d", ms.Scope(), ms.VisibleCount())
	}
	// Upstream handleInput toggles only on tui.input.tab; Shift+Tab reaches the search input and changes nothing.
	ms.HandleInput("\x1b[Z")
	if ms.Scope() != ModelScopeScoped || ms.searchInput.Text() != "" {
		t.Errorf("Shift+Tab changed scope=%v query=%q", ms.Scope(), ms.searchInput.Text())
	}
}

// TestModelSelector_ScopeTextMatchesUpstream: assert the literal
// header strings against upstream's getScopeText() output. Ground
// truth: ".upstream/current/packages/coding-agent/src/modes/
// interactive/components/model-selector.ts" lines 192-196 produces
// (after stripping theme.fg ANSI): "Scope: all | scoped".
func TestModelSelector_ScopeTextMatchesUpstream(t *testing.T) {
	ms := NewStaticModelSelectorComponent("x", mkItems("openai/gpt-4o"), mkItems("openai/gpt-4o", "groq/llama-3"), "")
	frame := ms.Render(80)
	// Rows 0-1 are the top DynamicBorder and Spacer(1).
	plain := stripANSI(frame[2])
	plain = strings.TrimRight(plain, " ")
	if !strings.HasPrefix(plain, "Scope: all | scoped") {
		t.Errorf("scope header mismatch with upstream: %q", plain)
	}
	// Hint line wording.
	hint := stripANSI(frame[3])
	if !strings.Contains(hint, "tab scope (all/scoped)") {
		t.Errorf("hint line mismatch with upstream getScopeHintText: %q", hint)
	}
}

func TestModelSelector_PreservesProviderSourceOrderWithoutCurrentModel(t *testing.T) {
	items := mkItems("fixture/model-two", "fixture/model-one")
	ms := NewStaticModelSelectorComponent("x", nil, items, "")
	frame := ms.Render(80)
	for _, line := range frame {
		plain := stripANSI(line)
		if strings.Contains(plain, "[fixture]") {
			if !strings.Contains(plain, "model-two") {
				t.Fatalf("first provider row = %q, want model-two", plain)
			}
			return
		}
	}
	t.Fatalf("no fixture model row in render:\n%s", strings.Join(frame, "\n"))
}

func TestModelSelector_CurrentModelPinnedFirst(t *testing.T) {
	all := mkItems("openai/gpt-4o-mini", "github-copilot/gpt-4o", "openai/gpt-4o")
	ms := NewStaticModelSelectorComponent("x", nil, all, "github-copilot/gpt-4o")
	frame := ms.Render(80)
	// Find the first row that's not header/filter/separator.
	for _, line := range frame {
		plain := stripANSI(line)
		if strings.Contains(plain, "[github-copilot]") || strings.Contains(plain, "[openai]") {
			if !strings.Contains(plain, "[github-copilot]") {
				t.Errorf("expected current model first, got: %q", plain)
			}
			return
		}
	}
	t.Errorf("no model row found in render:\n%s", strings.Join(frame, "\n"))
}

func TestModelSelector_FilterNarrows(t *testing.T) {
	scoped := mkItems("github-copilot/gpt-4o", "openai/gpt-4o-mini", "groq/llama-3.1-70b")
	ms := NewStaticModelSelectorComponent("x", scoped, scoped, "")
	if ms.VisibleCount() != 3 {
		t.Fatalf("baseline count wrong: %d", ms.VisibleCount())
	}
	for _, r := range "llama" {
		ms.HandleInput(string(r))
	}
	if ms.VisibleCount() != 1 {
		t.Errorf("expected filter `llama` → 1 match, got %d", ms.VisibleCount())
	}
}

func TestModelSelector_RefreshStatusAndErrorAreExclusive(t *testing.T) {
	ms := NewStaticModelSelectorComponent("x", nil, mkItems("fixture/model-one"), "")
	ms.SetStatus("Model catalogs refreshed.")
	if got := strings.Join(ms.Render(80), "\n"); !strings.Contains(got, "Model catalogs refreshed.") {
		t.Fatalf("status missing: %s", got)
	}
	ms.SetError("refresh failed")
	got := strings.Join(ms.Render(80), "\n")
	if !strings.Contains(got, "refresh failed") || strings.Contains(got, "Model catalogs refreshed.") {
		t.Fatalf("error/status render = %s", got)
	}
}

func TestModelSelector_FilterResetsSelectionToBestMatch(t *testing.T) {
	items := []ModelSelectorItem{
		{Provider: "fixture", ID: "alpha-1", Name: "Alpha One"},
		{Provider: "fixture", ID: "alpha-2", Name: "Alpha Two"},
		{Provider: "fixture", ID: "alpha-3", Name: "Alpha Three"},
		{Provider: "fixture", ID: "beta-1", Name: "Beta One"},
	}
	ms := NewStaticModelSelectorComponent("x", nil, items, "fixture/alpha-1")
	ms.HandleInput("\x1b[B")
	ms.HandleInput("\x1b[B")
	for _, r := range "alpha" {
		ms.HandleInput(string(r))
	}
	for _, line := range ms.Render(80) {
		plain := stripANSI(line)
		if strings.HasPrefix(plain, "→ ") {
			if !strings.Contains(plain, "alpha-1") {
				t.Fatalf("selected row = %q, want alpha-1", plain)
			}
			return
		}
	}
	t.Fatal("selected row not rendered")
}

func TestModelSelector_FilterUsesFuzzySubsequence(t *testing.T) {
	items := mkItems("fixture/model-one", "fixture/model-two")
	selector := NewStaticModelSelectorComponent("Select model", items, items, "")
	selector.SetFilter("mtw")
	joined := stripANSI(strings.Join(selector.Render(80), "\n"))
	if !strings.Contains(joined, "model-two") || strings.Contains(joined, "model-one") {
		t.Fatalf("fuzzy model filter:\n%s", joined)
	}
}

func TestModelSelector_SearchDelegatesCursorEditingToInput(t *testing.T) {
	items := mkItems("fixture/model-one", "fixture/model-two")
	selector := NewStaticModelSelectorComponent("Select model", items, items, "")
	selector.SetFilter("ab")
	selector.HandleInput("\x1b[D")
	selector.HandleInput("\x1b[H")
	selector.HandleInput("x")
	joined := stripANSI(strings.Join(selector.Render(80), "\n"))
	if !strings.Contains(joined, "> xab") {
		t.Fatalf("edited model filter:\n%s", joined)
	}
}

func TestModelSelectorArrowNavigationWrapsAtBothEnds(t *testing.T) {
	items := mkItems("fixture/one", "fixture/two", "fixture/three")
	up := NewStaticModelSelectorComponent("x", items, items, "")
	up.HandleInput("\x1b[A")
	up.HandleInput("\r")
	if got := up.SelectedFQ(); got != "fixture/three" {
		t.Fatalf("up from first selected %q, want fixture/three", got)
	}

	down := NewStaticModelSelectorComponent("x", items, items, "")
	down.HandleInput("\x1b[B")
	down.HandleInput("\x1b[B")
	down.HandleInput("\x1b[B")
	down.HandleInput("\r")
	if got := down.SelectedFQ(); got != "fixture/one" {
		t.Fatalf("down from last selected %q, want fixture/one", got)
	}
}

// Upstream model-selector.ts handleInput has no page actions: PageUp and PageDown reach the search input, leave the query empty, and keep the selection.
func TestModelSelectorPageKeysDoNotMoveSelection(t *testing.T) {
	items := make([]ModelSelectorItem, 15)
	for i := range items {
		items[i] = ModelSelectorItem{Provider: "fixture", ID: fmt.Sprintf("model-%02d", i)}
	}
	selector := NewStaticModelSelectorComponent("x", nil, items, "")
	selector.HandleInput("\x1b[B")
	for _, key := range []string{"\x1b[5~", "\x1b[6~", "\x1b[6~"} {
		selector.HandleInput(key)
		if selector.cursor != 1 || selector.searchInput.Text() != "" {
			t.Fatalf("%q moved selection to %d with query %q", key, selector.cursor, selector.searchInput.Text())
		}
	}
}

// Upstream handleInput passes every other key to the search input and then always calls filterModels, which selects the first row while a query is active, even when the key did not change the query.
func TestModelSelectorUnchangedQueryKeyReselectsFirstMatch(t *testing.T) {
	items := mkItems("fixture/model-two", "fixture/model-one")
	for _, key := range []string{"\x1b[D", "\x1b[5~", "\x1b[Z"} {
		selector := NewStaticModelSelectorComponent("x", nil, items, "fixture/model-two")
		for _, r := range "model" {
			selector.HandleInput(string(r))
		}
		selector.HandleInput("\x1b[B")
		if selector.cursor != 1 {
			t.Fatalf("down selected %d", selector.cursor)
		}
		selector.HandleInput(key)
		if selector.cursor != 0 || selector.searchInput.Text() != "model" {
			t.Errorf("%q left selection %d with query %q, want 0 with model", key, selector.cursor, selector.searchInput.Text())
		}
	}
}

// Upstream setScope selects the current model's index in the new unfiltered scope, or the first row when it is absent, and then filterModels selects the first row for an active query.
func TestModelSelectorScopeToggleSelectsCurrentOrFirst(t *testing.T) {
	scoped := mkItems("p/model-a", "p/model-c")
	all := mkItems("p/model-a", "p/model-b", "p/model-c", "p/model-d")

	query := NewStaticModelSelectorComponent("x", scoped, all, "p/model-c")
	for _, r := range "model" {
		query.HandleInput(string(r))
	}
	query.HandleInput("\t")
	query.HandleInput("\t")
	if query.Scope() != ModelScopeScoped || query.cursor != 0 {
		t.Errorf("scope toggle with a query selected %d in %v, want 0 in scoped", query.cursor, query.Scope())
	}

	absent := NewStaticModelSelectorComponent("x", scoped, all, "p/model-d")
	absent.HandleInput("\t")
	absent.HandleInput("\x1b[B")
	absent.HandleInput("\x1b[B")
	absent.HandleInput("\t")
	if absent.Scope() != ModelScopeScoped || absent.cursor != 0 {
		t.Errorf("scope toggle without current selected %d in %v, want 0 in scoped", absent.cursor, absent.Scope())
	}

	present := NewStaticModelSelectorComponent("x", scoped, all, "p/model-c")
	present.HandleInput("\x1b[A")
	present.HandleInput("\t")
	present.HandleInput("\t")
	if present.Scope() != ModelScopeScoped || present.cursor != 1 {
		t.Errorf("scope toggle selected %d in %v, want current at 1 in scoped", present.cursor, present.Scope())
	}
}

func TestModelSelector_EnterSelectsAndSetsFQ(t *testing.T) {
	scoped := mkItems("openai/gpt-4o", "openai/gpt-4o-mini")
	ms := NewStaticModelSelectorComponent("x", scoped, scoped, "")
	ms.HandleInput("\r")
	if !ms.Done() || ms.Cancelled() {
		t.Fatalf("expected done & not cancelled, got done=%v cancelled=%v", ms.Done(), ms.Cancelled())
	}
	if ms.SelectedFQ() != "openai/gpt-4o" {
		t.Errorf("expected SelectedFQ openai/gpt-4o, got %q", ms.SelectedFQ())
	}
}

func TestModelSelector_EscCancels(t *testing.T) {
	scoped := mkItems("openai/gpt-4o")
	ms := NewStaticModelSelectorComponent("x", scoped, scoped, "")
	ms.HandleInput("\x1b")
	if !ms.Cancelled() || !ms.Done() {
		t.Errorf("expected cancelled & done after Esc")
	}
}

// Upstream sortModels applies to the all-models list only; scoped models keep
// their configured order (model-selector.ts loadModelsFromSnapshot).
func TestModelSelector_ScopedModelsKeepConfiguredOrder(t *testing.T) {
	scoped := mkItems("openai/gpt-4o-mini", "github-copilot/gpt-4o")
	ms := NewStaticModelSelectorComponent("x", scoped, scoped, "github-copilot/gpt-4o")
	var rows []string
	for _, line := range ms.Render(80) {
		plain := strings.TrimRight(stripANSI(line), " ")
		if strings.Contains(plain, "[openai]") || strings.Contains(plain, "[github-copilot]") {
			rows = append(rows, plain)
		}
	}
	want := []string{"    gpt-4o-mini [openai]", "→ ✓ gpt-4o [github-copilot]"}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("scoped rows = %q, want %q", rows, want)
	}
}

// Pi 0.87.1 model-selector.ts renders the current model as a "✓ " prefix
// column after the cursor column (model-resolver-selector/04 probe).
func TestModelSelector_CurrentMarkerPrefixColumn(t *testing.T) {
	ms := NewStaticModelSelectorComponent("x", nil, mkItems("fixture/model-two", "fixture/model-one"), "fixture/model-two")
	frame := ms.Render(100)
	plain := make([]string, len(frame))
	for i, line := range frame {
		plain[i] = strings.TrimRight(stripANSI(line), " ")
	}
	want := []string{
		strings.Repeat("─", 100),
		"",
		"Only showing models from configured providers. Use /login to add providers.",
		"",
		">",
		"",
		"→ ✓ model-two [fixture]",
		"    model-one [fixture]",
		"",
		"  Model Name: model-two",
		"",
		"  Enter to select · Ctrl+S to set as default · Escape/Ctrl+C to cancel",
		strings.Repeat("─", 100),
	}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("render =\n%s\nwant\n%s", strings.Join(plain, "\n"), strings.Join(want, "\n"))
	}
	ms.HandleInput("\x1b[B")
	var rows []string
	for _, line := range ms.Render(100) {
		if p := strings.TrimRight(stripANSI(line), " "); strings.Contains(p, "[fixture]") {
			rows = append(rows, p)
		}
	}
	if want := []string{"  ✓ model-two [fixture]", "→   model-one [fixture]"}; strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("rows after Down = %q, want %q", rows, want)
	}
}

// Upstream badges the settings default model, sorts it after the current
// model, and lists it first for a "default" prefix search.
func TestModelSelector_DefaultModelBadgeSortAndSearch(t *testing.T) {
	all := mkItems("anthropic/claude", "openai/gpt", "zai/glm")
	ms := NewStaticModelSelectorComponent("x", nil, all, "openai/gpt")
	ms.SetDefaultModel("zai/glm")
	var rows []string
	for _, line := range ms.Render(80) {
		if p := strings.TrimRight(stripANSI(line), " "); strings.Contains(p, "[") && !strings.Contains(p, "Only showing") {
			rows = append(rows, p)
		}
	}
	want := []string{"→ ✓ gpt [openai]", "    glm [zai] · default", "    claude [anthropic]"}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
	ms.SetFilter("def")
	ms.HandleInput("\r")
	if got := ms.SelectedFQ(); got != "zai/glm" {
		t.Fatalf("\"def\" search selected %q, want the default model", got)
	}
}

// Upstream renders every row as Text(..., 0, 0), so a long row wraps instead
// of being truncated.
func TestModelSelector_LongRowsWrap(t *testing.T) {
	ms := NewStaticModelSelectorComponent("x", nil, mkItems("provider/a-very-long-model-identifier"), "")
	joined := stripANSI(strings.Join(ms.Render(20), "\n"))
	if !strings.Contains(joined, "a-very-long-model-id\nentifier [provider]") {
		t.Fatalf("long row was truncated:\n%s", joined)
	}
	for _, line := range ms.Render(20) {
		if w := widthx.VisibleWidth(line); w > 20 {
			t.Fatalf("row overflows width 20 (%d): %q", w, line)
		}
	}
}

func TestModelSelector_RefreshSuccessUsesSuccessColor(t *testing.T) {
	ms := NewStaticModelSelectorComponent("x", nil, mkItems("fixture/model-one"), "")
	ms.SetRefreshSuccess("Model catalogs refreshed.")
	want := fg(ActiveTheme().Success, "  Model catalogs refreshed.")
	if joined := strings.Join(ms.Render(80), "\n"); !strings.Contains(joined, want) {
		t.Fatalf("refresh success row missing success color:\n%q", joined)
	}
}

// ModelSelectorComponent extends Container (model-selector.ts:40): border, spacer, scope text, scope hint, spacer, search
// input, spacer, list container, spacer, hint, border (a single warning Text replaces the scope pair without scoped models);
// a key rebuilds the list container's rows (updateList).
func TestModelSelectorComponentChildrenFollowUpstream(t *testing.T) {
	items := []ModelSelectorItem{{Provider: "p", ID: "a", Name: "A"}, {Provider: "p", ID: "b", Name: "B"}}
	scoped := NewStaticModelSelectorComponent("T", items, items, "")
	if got := len(scoped.Children()); got != 11 {
		t.Fatalf("scoped children = %d, want 11", got)
	}
	if got := len(NewStaticModelSelectorComponent("T", nil, items, "").Children()); got != 10 {
		t.Fatalf("children without scoped models = %d, want 10", got)
	}
	before := strings.Join(scoped.Render(60), "\n")
	scoped.HandleInput("\x1b[B")
	if strings.Join(scoped.Render(60), "\n") == before {
		t.Fatal("moving the cursor did not change the render")
	}
	scoped.HandleInput("\t")
	if !strings.Contains(stripANSI(strings.Join(scoped.Render(60), "\n")), "Scope: all | scoped") {
		t.Fatal("scope header missing")
	}
}

// model-selector.ts:getSearchInput returns the live search input: text typed into the selector reaches it, a value set on it filters the list, and focus reaches it.
// Pi: packages/coding-agent/src/modes/interactive/components/model-selector.ts:418 (ModelSelectorComponent.getSearchInput).
func TestModelSelectorGetSearchInputIsTheLiveInput(t *testing.T) {
	all := mkItems("openai/gpt-4o", "anthropic/claude-3", "openrouter/llama-3")
	ms := NewStaticModelSelectorComponent("Select model", nil, all, "")
	input := ms.GetSearchInput()
	if input == nil || input.GetValue() != "" {
		t.Fatalf("a new selector has an empty search input, got %+v", input)
	}
	for _, r := range "llama" {
		ms.HandleInput(string(r))
	}
	if got := input.GetValue(); got != "llama" {
		t.Errorf("typed text reaches the search input, got %q", got)
	}
	if got := ms.VisibleCount(); got != 1 {
		t.Errorf("typing filters the list to llama-3, got %d visible", got)
	}
	input.SetValue("")
	ms.HandleInput("o")
	if got := input.GetValue(); got != "o" {
		t.Errorf("the same input keeps receiving text after SetValue, got %q", got)
	}
	ms.SetFocused(true)
	if !input.Focused {
		t.Errorf("SetFocused reaches the search input")
	}
}
