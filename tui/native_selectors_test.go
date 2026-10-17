package tui

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// nselNode returns c's node as a selector.
func nselNode(t *testing.T, c NativeComponent) frontend.Selector {
	t.Helper()
	node, ok := c.NativeNode()
	if !ok {
		t.Fatalf("%T reports no node", c)
	}
	sel, ok := node.(frontend.Selector)
	if !ok {
		t.Fatalf("%T reports %T, want a selector", c, node)
	}
	return sel
}

// nselItemIDs returns the ids of sel's items in order.
func nselItemIDs(sel frontend.Selector) []string {
	ids := make([]string, len(sel.Items))
	for i, item := range sel.Items {
		ids[i] = item.ID
	}
	return ids
}

// nselMount mounts c in a container dock of a started surface and returns
// the id of the selector the dock reports in the editor's place.
func nselMount(t *testing.T, c Component) (*TuiSurface, *recordingSession, string) {
	t.Helper()
	surface, session := startedSurface(t, NewContainer(), NewContainer(c))
	id := nselDockSelector(session)
	if id == "" {
		t.Fatalf("dock ids = %q, want a selector", dockIDs(session))
	}
	return surface, session, id
}

// nselDockSelector returns the id of the dock's selector node, or "".
func nselDockSelector(session *recordingSession) string {
	for _, entry := range session.tree[frontend.RegionDock] {
		if strings.HasPrefix(entry.id, "selector.") {
			return entry.id
		}
	}
	return ""
}

// nselShown returns the selector node the session holds as id.
func nselShown(t *testing.T, session *recordingSession, id string) frontend.Selector {
	t.Helper()
	for _, entry := range session.tree[frontend.RegionDock] {
		if entry.id == id {
			return entry.node.(frontend.Selector)
		}
	}
	t.Fatalf("dock ids = %q, want %s", dockIDs(session), id)
	return frontend.Selector{}
}

// nselAct performs action through the surface's keys, feeding each to c and
// rendering after each step, and returns the number of keys sent.
func nselAct(t *testing.T, surface *TuiSurface, c interface{ HandleInput(string) }, action frontend.Action) int {
	t.Helper()
	a := NewNativeAction(action)
	sent := 0
	for range 64 {
		keys := surface.NativeKeys(a)
		if keys == nil {
			return sent
		}
		for _, key := range keys {
			c.HandleInput(key)
			sent++
		}
		surface.Render()
	}
	t.Fatalf("action %+v did not finish", action)
	return sent
}

func newNativeTestModelSelector() *ModelSelectorComponent {
	ms := NewStaticModelSelectorComponent("Select model", mkItems("a/m1", "b/m2"), mkItems("a/m1", "b/m2", "c/m3"), "b/m2")
	ms.SetDefaultModel("a/m1")
	return ms
}

func TestNativeModelSelectorNode(t *testing.T) {
	ms := newNativeTestModelSelector()
	got := nselNode(t, ms)
	want := frontend.Selector{
		Title:      "Select model",
		Searchable: true,
		Items: []frontend.SelectorItem{
			{ID: "a/m1", Label: "m1", Detail: "a · default"},
			{ID: "b/m2", Label: "m2", Detail: "b", Checked: true},
		},
		Selected: "b/m2",
		Tabs:     []frontend.SelectorTab{{ID: "all", Label: "all"}, {ID: "scoped", Label: "scoped"}},
		Tab:      "scoped",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node = %#v\nwant %#v", got, want)
	}

	ms.SetError("catalog failed")
	if got := nselNode(t, ms).Status; got != "catalog failed" {
		t.Fatalf("error status = %q", got)
	}
	ms.SetRefreshSuccess("Model catalogs refreshed.")
	if got := nselNode(t, ms).Status; got != "Model catalogs refreshed." {
		t.Fatalf("refresh status = %q", got)
	}

	noAuth := NewStaticModelSelectorComponent("Select model", nil, mkItems("a/m1", "b/m2"), "")
	sel := nselNode(t, noAuth)
	if sel.Description != "Only showing models from configured providers. Use /login to add providers." || sel.Tabs != nil || sel.Tab != "" {
		t.Fatalf("no-auth node = %#v", sel)
	}
	if got := nselItemIDs(sel); !slices.Equal(got, []string{"a/m1", "b/m2"}) || sel.Selected != "a/m1" {
		t.Fatalf("no-auth items = %q selected %q", got, sel.Selected)
	}
}

func TestNativeModelSelectorFollowsKeys(t *testing.T) {
	ms := newNativeTestModelSelector()
	ms.HandleInput("\x1b[A")
	if got := nselNode(t, ms).Selected; got != "a/m1" {
		t.Fatalf("selected after up = %q", got)
	}
	ms.HandleInput("\t")
	sel := nselNode(t, ms)
	if sel.Tab != "all" || !slices.Equal(nselItemIDs(sel), []string{"b/m2", "a/m1", "c/m3"}) || sel.Selected != "b/m2" {
		t.Fatalf("after tab: %#v", sel)
	}
	for _, key := range []string{"m", "3"} {
		ms.HandleInput(key)
	}
	sel = nselNode(t, ms)
	if sel.Query != "m3" || !slices.Equal(nselItemIDs(sel), []string{"c/m3"}) || sel.Selected != "c/m3" {
		t.Fatalf("after typing: %#v", sel)
	}
	ms.HandleInput("\x7f")
	if got := nselNode(t, ms).Query; got != "m" {
		t.Fatalf("query after backspace = %q", got)
	}
	ms.HandleInput("\r")
	if !ms.Done() || ms.SelectedFQ() == "" {
		t.Fatalf("confirm: done %v selected %q", ms.Done(), ms.SelectedFQ())
	}
}

func TestNativeModelSelectorKeysRoundTrip(t *testing.T) {
	ms := newNativeTestModelSelector()
	surface, session, id := nselMount(t, ms)
	if got := nselShown(t, session, id); got.Selected != "b/m2" || got.Tab != "scoped" {
		t.Fatalf("shown = %#v", got)
	}

	// First item, then back to the last.
	if sent := nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "a/m1"}); sent != 1 {
		t.Fatalf("keys to the first item = %d", sent)
	}
	if got := nselShown(t, session, id).Selected; got != "a/m1" {
		t.Fatalf("highlighted %q", got)
	}
	nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "b/m2"})
	if got := nselShown(t, session, id).Selected; got != "b/m2" {
		t.Fatalf("highlighted %q", got)
	}

	// The scope tab, then a model only that scope lists.
	nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.SwitchTab, Item: "all"})
	if got := nselShown(t, session, id); got.Tab != "all" || len(got.Items) != 3 {
		t.Fatalf("after switching tab: %#v", got)
	}
	if sent := nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.SwitchTab, Item: "all"}); sent != 0 {
		t.Fatalf("keys to the shown tab = %d", sent)
	}

	// A filter that matches nothing leaves nothing to highlight.
	nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Filter, Value: "zzz"})
	if got := nselShown(t, session, id); got.Query != "zzz" || len(got.Items) != 0 || got.Selected != "" {
		t.Fatalf("empty filter: %#v", got)
	}
	if sent := nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "c/m3"}); sent != 0 {
		t.Fatalf("highlight in an empty list sent %d keys", sent)
	}
	nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Filter, Value: ""})
	if got := nselShown(t, session, id); got.Query != "" || len(got.Items) != 3 {
		t.Fatalf("cleared filter: %#v", got)
	}

	nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Choose, Item: "c/m3"})
	if !ms.Done() || ms.Cancelled() || ms.SelectedFQ() != "c/m3" {
		t.Fatalf("choose: done %v cancelled %v selected %q", ms.Done(), ms.Cancelled(), ms.SelectedFQ())
	}
}

func TestNativeModelSelectorKeysSingletonAndDismiss(t *testing.T) {
	ms := NewStaticModelSelectorComponent("Select model", mkItems("a/m1"), mkItems("a/m1", "b/m2"), "")
	surface, session, id := nselMount(t, ms)
	if got := nselShown(t, session, id); !slices.Equal(nselItemIDs(got), []string{"a/m1"}) || got.Selected != "a/m1" {
		t.Fatalf("singleton: %#v", got)
	}
	if sent := nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "a/m1"}); sent != 0 {
		t.Fatalf("highlighting the only item sent %d keys", sent)
	}
	nselAct(t, surface, ms, frontend.Action{Node: id, Kind: frontend.Choose})
	if ms.SelectedFQ() != "a/m1" {
		t.Fatalf("chose %q", ms.SelectedFQ())
	}

	other := NewStaticModelSelectorComponent("Select model", mkItems("a/m1"), mkItems("a/m1"), "")
	surface, _, id = nselMount(t, other)
	nselAct(t, surface, other, frontend.Action{Node: id, Kind: frontend.Dismiss})
	if !other.Cancelled() || !other.Done() {
		t.Fatalf("dismiss: cancelled %v done %v", other.Cancelled(), other.Done())
	}
}

func newNativeTestScopedModels() *ScopedModelsList {
	return NewScopedModelsList(ScopedModelsConfig{
		AllModels: []ModelItem{
			{FullID: "a/m1", Name: "Model One", Provider: "a"},
			{FullID: "b/m2", Name: "Model Two", Provider: "b"},
		},
		EnabledModelIDs: []string{"a/m1", "x/gone"},
		RefreshStatus:   "Model catalogs refreshed.",
	})
}

func TestNativeScopedModelsListNode(t *testing.T) {
	s := newNativeTestScopedModels()
	got := nselNode(t, s)
	want := frontend.Selector{
		Title:       "Model Configuration",
		Description: "Session-only. " + ActionKeyDisplayTextOr("app.models.save", "ctrl+s") + " to save to settings.",
		Searchable:  true,
		Items: []frontend.SelectorItem{
			{ID: "a/m1", Label: "m1", Detail: "a", Checked: true},
			{ID: "x/gone", Label: "x/gone", Detail: "unavailable"},
			{ID: "b/m2", Label: "m2", Detail: "b"},
		},
		Selected: "a/m1",
		Status:   "Model catalogs refreshed.\n1/2 enabled · 1 unavailable",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node = %#v\nwant %#v", got, want)
	}
	all := NewScopedModelsList(ScopedModelsConfig{AllModels: []ModelItem{{FullID: "a/m1", Provider: "a"}}})
	if sel := nselNode(t, all); sel.Status != "all enabled" || !sel.Items[0].Checked {
		t.Fatalf("all enabled node = %#v", sel)
	}
}

func TestNativeScopedModelsListFollowsKeys(t *testing.T) {
	s := newNativeTestScopedModels()
	s.HandleInput("\x1b[B")
	s.HandleInput("\x1b[B")
	if got := nselNode(t, s).Selected; got != "b/m2" {
		t.Fatalf("selected after two downs = %q", got)
	}
	s.HandleInput("\r")
	sel := nselNode(t, s)
	if !sel.Items[2].Checked || sel.Status != "Model catalogs refreshed.\n2/2 enabled · 1 unavailable (unsaved)" {
		t.Fatalf("after toggle: %#v", sel)
	}
	s.HandleInput("\x13")
	if got := nselNode(t, s).Status; got != "Model catalogs refreshed.\n2/2 enabled · 1 unavailable" {
		t.Fatalf("after save status = %q", got)
	}
	s.HandleInput("m")
	s.HandleInput("2")
	if sel := nselNode(t, s); sel.Query != "m2" || !slices.Equal(nselItemIDs(sel), []string{"b/m2"}) {
		t.Fatalf("after typing: %#v", sel)
	}
	s.HandleInput("\x1b")
	if !s.Done() || !s.Result().Cancelled {
		t.Fatalf("escape: %#v", s.Result())
	}
}

func TestNativeScopedModelsListKeysRoundTrip(t *testing.T) {
	s := newNativeTestScopedModels()
	surface, session, id := nselMount(t, s)
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "b/m2"})
	if got := nselShown(t, session, id).Selected; got != "b/m2" {
		t.Fatalf("highlighted last %q", got)
	}
	// Disabling the first model moves it after the enabled ones.
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Choose, Item: "a/m1"})
	if got := nselShown(t, session, id); !slices.Equal(nselItemIDs(got), []string{"x/gone", "a/m1", "b/m2"}) || got.Items[1].Checked || !strings.HasSuffix(got.Status, "(unsaved)") {
		t.Fatalf("after choosing the first: %#v", got)
	}
	if s.Done() {
		t.Fatal("toggling closed the list")
	}

	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Filter, Value: "m2"})
	if got := nselShown(t, session, id); !slices.Equal(nselItemIDs(got), []string{"b/m2"}) || got.Selected != "b/m2" {
		t.Fatalf("filtered to one: %#v", got)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Choose})
	if got := nselShown(t, session, id); !got.Items[0].Checked {
		t.Fatalf("choosing the only item: %#v", got)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Filter, Value: "zzz"})
	if got := nselShown(t, session, id); len(got.Items) != 0 {
		t.Fatalf("empty filter: %#v", got)
	}
	if sent := nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "a/m1"}); sent != 0 {
		t.Fatalf("highlight in an empty list sent %d keys", sent)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Dismiss})
	if !s.Done() || !s.Result().Cancelled {
		t.Fatalf("dismiss: %#v", s.Result())
	}
}

// nativeUserMessageRecorder is a fork picker whose confirm and cancel callbacks are recorded, as the host observes them.
type nativeUserMessageRecorder struct {
	*UserMessageSelectorComponent
	selected  []string
	cancelled int
}

func newNativeUserMessageRecorder(texts []string) *nativeUserMessageRecorder {
	r := &nativeUserMessageRecorder{}
	items := make([]UserMessageItem, len(texts))
	for i, text := range texts {
		items[i] = UserMessageItem{ID: "entry-" + strconv.Itoa(i), Text: text}
	}
	r.UserMessageSelectorComponent = NewUserMessageSelectorComponent(items, func(id string) { r.selected = append(r.selected, id) }, func() { r.cancelled++ }, "")
	return r
}

func TestNativeUserMessageSelectorNode(t *testing.T) {
	messages := []string{"first\nline", "\x1b[1msecond\x1b[22m", "third"}
	s := newNativeUserMessageRecorder(messages)
	got := nselNode(t, s)
	want := frontend.Selector{
		Title:       "Fork from Message",
		Description: "Select a user message to copy the active path up to that point into a new session",
		Items: []frontend.SelectorItem{
			{ID: "0", Label: normalizeToSingleLine(messages[0]), Detail: "Message 1 of 3"},
			{ID: "1", Label: "second", Detail: "Message 2 of 3"},
			{ID: "2", Label: "third", Detail: "Message 3 of 3"},
		},
		Selected: "2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node = %#v\nwant %#v", got, want)
	}
	if strings.Contains(got.Items[0].Label, "\n") {
		t.Fatalf("label %q spans lines", got.Items[0].Label)
	}

	s.HandleInput("\x1b[B")
	if got := nselNode(t, s).Selected; got != "0" {
		t.Fatalf("selected after wrapping down = %q", got)
	}
	s.HandleInput("\r")
	if !slices.Equal(s.selected, []string{"entry-0"}) {
		t.Fatalf("confirm reported %v, want [entry-0]", s.selected)
	}

	empty := nselNode(t, newNativeUserMessageRecorder(nil))
	if len(empty.Items) != 0 || empty.Selected != "" || empty.Status != "No user messages found" {
		t.Fatalf("empty node = %#v", empty)
	}
}

func TestNativeUserMessageSelectorKeysRoundTrip(t *testing.T) {
	s := newNativeUserMessageRecorder([]string{"one", "two", "three"})
	surface, session, id := nselMount(t, s)
	if sent := nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "0"}); sent != 2 {
		t.Fatalf("keys from the last to the first = %d", sent)
	}
	if got := nselShown(t, session, id).Selected; got != "0" {
		t.Fatalf("highlighted %q", got)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Choose, Item: "1"})
	if !slices.Equal(s.selected, []string{"entry-1"}) || s.cancelled != 0 {
		t.Fatalf("choose: selected %v cancelled %d", s.selected, s.cancelled)
	}

	single := newNativeUserMessageRecorder([]string{"only"})
	surface, _, id = nselMount(t, single)
	nselAct(t, surface, single, frontend.Action{Node: id, Kind: frontend.Choose, Item: "0"})
	if !slices.Equal(single.selected, []string{"entry-0"}) {
		t.Fatalf("singleton choose: selected %v", single.selected)
	}

	empty := newNativeUserMessageRecorder(nil)
	surface, _, id = nselMount(t, empty)
	if sent := nselAct(t, surface, empty, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "0"}); sent != 0 {
		t.Fatalf("highlight in an empty list sent %d keys", sent)
	}
	nselAct(t, surface, empty, frontend.Action{Node: id, Kind: frontend.Dismiss})
	if empty.cancelled == 0 {
		t.Fatal("dismiss did not cancel")
	}
}

func nativeTestAuthProviders() []OAuthProvider {
	return []OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth", Status: testAuthCheck{"oauth", ""}},
		{ID: "openai", Name: "OpenAI", AuthType: "oauth"},
	}
}

func TestNativeOAuthSelectorNode(t *testing.T) {
	got := nselNode(t, NewOAuthSelectorComponent("login", nativeTestAuthProviders(), nil, nil))
	want := frontend.Selector{
		Title:      "Select provider to configure:",
		Searchable: true,
		Items: []frontend.SelectorItem{
			{ID: "anthropic", Label: "Anthropic", Detail: "✓ configured"},
			{ID: "openai", Label: "OpenAI", Detail: "• not configured"},
		},
		Selected: "anthropic",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node = %#v\nwant %#v", got, want)
	}

	mixed := append(nativeTestAuthProviders(), OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key", Status: testAuthCheck{"api_key", "OPENAI_API_KEY"}})
	sel := nselNode(t, NewOAuthSelectorComponent("logout", mixed, nil, nil))
	if sel.Title != "Select provider to logout:" {
		t.Fatalf("logout title = %q", sel.Title)
	}
	wantItems := []frontend.SelectorItem{
		{ID: "anthropic:oauth", Label: "Anthropic", Detail: "[subscription] ✓ configured"},
		{ID: "openai:oauth", Label: "OpenAI", Detail: "[subscription] • not configured"},
		{ID: "openai:api_key", Label: "OpenAI", Detail: "[API key] ✓ env: OPENAI_API_KEY"},
	}
	if !reflect.DeepEqual(sel.Items, wantItems) {
		t.Fatalf("mixed items = %#v", sel.Items)
	}

	for _, tc := range []struct{ mode, status string }{
		{"login", "No providers available"},
		{"logout", "No providers logged in. Use /login first."},
	} {
		if sel := nselNode(t, NewOAuthSelectorComponent(tc.mode, nil, nil, nil)); sel.Status != tc.status || len(sel.Items) != 0 {
			t.Fatalf("%s empty node = %#v", tc.mode, sel)
		}
	}
	filtered := NewOAuthSelectorComponent("login", nativeTestAuthProviders(), nil, nil, "zzz")
	if sel := nselNode(t, filtered); sel.Query != "zzz" || sel.Status != "No matching providers" || sel.Selected != "" {
		t.Fatalf("unmatched node = %#v", sel)
	}
}

func TestNativeOAuthSelectorFollowsKeys(t *testing.T) {
	s := NewOAuthSelectorComponent("login", nativeTestAuthProviders(), nil, nil)
	s.HandleInput("\x1b[B")
	s.HandleInput("\x1b[B")
	if got := nselNode(t, s).Selected; got != "openai" {
		t.Fatalf("selected after clamped downs = %q", got)
	}
	for _, key := range []string{"o", "p", "e", "n"} {
		s.HandleInput(key)
	}
	if sel := nselNode(t, s); sel.Query != "open" || !slices.Equal(nselItemIDs(sel), []string{"openai"}) || sel.Selected != "openai" {
		t.Fatalf("after typing: %#v", sel)
	}
	s.HandleInput("\x1b")
	if !s.Cancelled() {
		t.Fatal("escape did not cancel")
	}
}

func TestNativeOAuthSelectorKeysRoundTrip(t *testing.T) {
	s := NewOAuthSelectorComponent("login", nativeTestAuthProviders(), nil, nil)
	surface, session, id := nselMount(t, s)
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "openai"})
	if got := nselShown(t, session, id).Selected; got != "openai" {
		t.Fatalf("highlighted the last: %q", got)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "anthropic"})
	if got := nselShown(t, session, id).Selected; got != "anthropic" {
		t.Fatalf("highlighted the first: %q", got)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Filter, Value: "zzz"})
	if sent := nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "openai"}); sent != 0 {
		t.Fatalf("highlight in an empty list sent %d keys", sent)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Filter, Value: "open"})
	if got := nselShown(t, session, id); got.Query != "open" || !slices.Equal(nselItemIDs(got), []string{"openai"}) {
		t.Fatalf("filtered to one: %#v", got)
	}
	nselAct(t, surface, s, frontend.Action{Node: id, Kind: frontend.Choose, Item: "openai"})
	if !s.Done() || s.SelectedID() != "openai" {
		t.Fatalf("choose: done %v selected %q", s.Done(), s.SelectedID())
	}

	var chosenID, chosenType string
	mixed := NewOAuthSelectorComponent("login", []OAuthProvider{
		{ID: "openai", Name: "OpenAI", AuthType: "oauth"},
		{ID: "openai", Name: "OpenAI", AuthType: "api_key"},
	}, func(providerID, authType string) { chosenID, chosenType = providerID, authType }, nil)

	surface, _, id = nselMount(t, mixed)
	nselAct(t, surface, mixed, frontend.Action{Node: id, Kind: frontend.Choose, Item: "openai:api_key"})
	if chosenID != "openai" || chosenType != "api_key" {
		t.Fatalf("mixed choose = %q %q", chosenID, chosenType)
	}
}

// nselTreeNode is a session tree entry with filter tags and a branch label.
type nselTreeNode struct {
	id, label, branch string
	tags              []string
	kids              []TreeNode
}

func (n *nselTreeNode) NodeID() string           { return n.id }
func (n *nselTreeNode) NodeLabel() string        { return n.label }
func (n *nselTreeNode) NodeChildren() []TreeNode { return n.kids }
func (n *nselTreeNode) NodeFilterTags() []string { return n.tags }
func (n *nselTreeNode) NodeBranchLabel() string  { return n.branch }
func (n *nselTreeNode) SetNodeBranchLabel(label, _ string) {
	n.branch = label
}

// newNativeTestTree returns a tree whose root user entry branches into a
// labeled user entry and a tool result, on the tool result's path.
func newNativeTestTree(t *testing.T) *TreeSelectorComponent {
	t.Helper()
	treeHelpTestKeybindings(t, nil)
	root := &nselTreeNode{kids: []TreeNode{
		&nselTreeNode{id: "r", label: "\x1b[1mstart\x1b[22m", tags: []string{"user"}, kids: []TreeNode{
			&nselTreeNode{id: "a", label: "alpha", branch: "keep", tags: []string{"user", "labeled"}},
			&nselTreeNode{id: "b", label: "beta", tags: []string{"tool_result"}},
		}},
	}}
	ts := NewTreeSelectorComponent("", root)
	ts.SetInitialCursor("b", "")
	return ts
}

func TestNativeTreeSelectNode(t *testing.T) {
	ts := newNativeTestTree(t)
	got := nselNode(t, ts)
	want := frontend.Selector{
		Title:      "Session Tree",
		Searchable: true,
		Items: []frontend.SelectorItem{
			{ID: "r", Label: "start", Checked: true},
			{ID: "b", Label: "beta", Checked: true, Depth: 1},
			{ID: "a", Label: "[keep] alpha", Depth: 1},
		},
		Selected: "b",
		Tabs: []frontend.SelectorTab{
			{ID: "default", Label: "default"}, {ID: "no-tools", Label: "no-tools"}, {ID: "user-only", Label: "user-only"},
			{ID: "labeled-only", Label: "labeled-only"}, {ID: "all", Label: "all"},
		},
		Tab: "default",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node = %#v\nwant %#v", got, want)
	}
	if got := ts.nativeTabKey(); got != "app.tree.filter.cycleForward" {
		t.Fatalf("tab key = %q", got)
	}
}

func TestNativeTreeSelectFollowsKeys(t *testing.T) {
	ts := newNativeTestTree(t)
	ts.HandleInput("\x1b[A")
	if got := nselNode(t, ts).Selected; got != "r" {
		t.Fatalf("selected after up = %q", got)
	}
	ts.HandleInput("\x0f") // ctrl+o: cycle forward
	sel := nselNode(t, ts)
	if sel.Tab != "no-tools" || sel.Status != "[no-tools]" || !slices.Equal(nselItemIDs(sel), []string{"r", "a"}) {
		t.Fatalf("after cycling: %#v", sel)
	}
	ts.HandleInput("T")
	if got := nselNode(t, ts).Status; got != "[no-tools] [+label time]" {
		t.Fatalf("status with label time = %q", got)
	}
	ts.HandleInput("a")
	ts.HandleInput("l")
	if sel := nselNode(t, ts); sel.Query != "al" || !slices.Equal(nselItemIDs(sel), []string{"a"}) || sel.Selected != "a" {
		t.Fatalf("after typing: %#v", sel)
	}
	ts.HandleInput("\x7f")
	if got := nselNode(t, ts).Query; got != "a" {
		t.Fatalf("query after backspace = %q", got)
	}

	// The label editor is not modelled.
	ts.HandleInput("L")
	if _, ok := ts.NativeNode(); ok {
		t.Fatal("node reported while the label editor is open")
	}
	ts.HandleInput("\x1b")
	if _, ok := ts.NativeNode(); !ok {
		t.Fatal("no node after the label editor closed")
	}
	ts.HandleInput("\r")
	if !ts.Done() || ts.SelectedID() != "a" {
		t.Fatalf("confirm: done %v selected %q", ts.Done(), ts.SelectedID())
	}
}

func TestNativeTreeSelectKeysRoundTrip(t *testing.T) {
	ts := newNativeTestTree(t)
	surface, session, id := nselMount(t, ts)
	nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "r"})
	nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "a"})
	if got := nselShown(t, session, id).Selected; got != "a" {
		t.Fatalf("highlighted the last: %q", got)
	}

	if sent := nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.SwitchTab, Item: "user-only"}); sent != 2 {
		t.Fatalf("keys to the third filter = %d", sent)
	}
	if got := nselShown(t, session, id); got.Tab != "user-only" || !slices.Equal(nselItemIDs(got), []string{"r", "a"}) {
		t.Fatalf("after switching tab: %#v", got)
	}

	nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.Filter, Value: "zzz"})
	if got := nselShown(t, session, id); got.Query != "zzz" || len(got.Items) != 0 {
		t.Fatalf("empty filter: %#v", got)
	}
	if sent := nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.Highlight, Item: "r"}); sent != 0 {
		t.Fatalf("highlight in an empty list sent %d keys", sent)
	}
	nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.Filter, Value: "alp"})
	if got := nselShown(t, session, id); !slices.Equal(nselItemIDs(got), []string{"a"}) || got.Selected != "a" {
		t.Fatalf("filtered to one: %#v", got)
	}

	// Dismiss with a query clears it, as Escape does; the next closes.
	nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.Dismiss})
	if got := nselShown(t, session, id); ts.Done() || got.Query != "" {
		t.Fatalf("first dismiss: done %v node %#v", ts.Done(), got)
	}
	nselAct(t, surface, ts, frontend.Action{Node: id, Kind: frontend.Choose, Item: "r"})
	if !ts.Done() || ts.SelectedID() != "r" {
		t.Fatalf("choose: done %v selected %q", ts.Done(), ts.SelectedID())
	}

	other := newNativeTestTree(t)
	surface, session, _ = nselMount(t, other)
	other.HandleInput("L")
	surface.Render()
	if got := nselDockSelector(session); got != "" {
		t.Fatalf("label editor reported as %s", got)
	}
	other.HandleInput("\x1b")
	surface.Render()
	id = nselDockSelector(session)
	nselAct(t, surface, other, frontend.Action{Node: id, Kind: frontend.Dismiss})
	if !other.Cancelled() {
		t.Fatal("dismiss did not cancel")
	}
}

func BenchmarkNativeModelSelectorFilter(b *testing.B) {
	models := make([]ModelSelectorItem, 2000)
	for i := range models {
		models[i] = ModelSelectorItem{Provider: fmt.Sprintf("provider-%d", i%20), ID: fmt.Sprintf("model-%d", i)}
	}
	ms := NewStaticModelSelectorComponent("Select model", nil, models, models[0].FQ())
	b.ReportAllocs()
	for b.Loop() {
		ms.HandleInput("7")
		ms.HandleInput("\x7f")
		if _, ok := ms.NativeNode(); !ok {
			b.Fatal("no node")
		}
	}
}
