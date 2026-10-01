package mcpext_test

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The manager view (.upstream/v0.99.1/packages/coding-agent/src/extensions/mcp/ui.ts) and the manager of
// extensions/mcp/index.ts:515-701. Upstream has no test file for either; the expectations are their layout, key
// handling and menus.

type renderCounter struct{ n atomic.Int64 }

func (r *renderCounter) RequestRender() { r.n.Add(1) }

type managerHarness struct {
	view    *mcpext.McpManagerView
	renders *renderCounter
}

func newManagerHarness(t *testing.T) *managerHarness {
	t.Helper()
	h := &managerHarness{renders: &renderCounter{}}
	h.view = mcpext.NewMcpManagerView(h.renders, coloredTheme(t), tui.GetTUIKeybindings())
	return h
}

func (h *managerHarness) rows(width int) []string {
	rows := h.view.Render(width)
	plain := make([]string, len(rows))
	for i, row := range rows {
		plain[i] = strings.TrimRight(stripSGR(row), " ")
	}
	return plain
}

func (h *managerHarness) text(t *testing.T) string {
	t.Helper()
	return strings.Join(h.rows(80), "\n")
}

func (h *managerHarness) waitForText(t *testing.T, want string) {
	t.Helper()
	waitFor(t, "the view to show "+want, func() bool { return strings.Contains(h.text(t), want) })
}

func (h *managerHarness) type_(text string) {
	for _, r := range text {
		h.view.HandleInput(string(r))
	}
}

type menuAnswer struct {
	value string
	ok    bool
}

func (h *managerHarness) menu(ctx context.Context, build func() mcpext.McpMenu, subscribe func(func()) func()) <-chan menuAnswer {
	answer := make(chan menuAnswer, 1)
	go func() {
		value, ok := h.view.Menu(ctx, build, subscribe)
		answer <- menuAnswer{value, ok}
	}()
	return answer
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func abMenu() mcpext.McpMenu {
	return mcpext.McpMenu{
		Title: "Pick", Details: "the details", Error: "the error",
		Items:        []tui.SelectItem{{Value: "a", Label: "alpha", Description: "first"}, {Value: "b", Label: "beta", Description: "second"}},
		ConfirmLabel: "manage", CancelLabel: "close",
	}
}

// ui.ts:75-77: the view shows "Loading…" in the frame until the manager shows its first menu.
func TestMcpManagerViewStartsAsALoadingFrame(t *testing.T) {
	h := newManagerHarness(t)
	rows := h.rows(40)
	want := []string{strings.Repeat("─", 40), " MCP servers", "", " Loading…", "", strings.Repeat("─", 40)}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
}

// ui.ts:54-64,109-141: a menu is a frame with the title, details, error, a spacer, the list and the key hints.
func TestMcpManagerViewMenuLayout(t *testing.T) {
	h := newManagerHarness(t)
	answer := h.menu(t.Context(), abMenu, nil)
	h.waitForText(t, "alpha")
	rows := h.rows(60)
	if rows[0] != strings.Repeat("─", 60) || rows[1] != " Pick" || rows[2] != " the details" || rows[3] != " the error" || rows[4] != "" {
		t.Fatalf("head = %q", rows[:5])
	}
	n := len(rows)
	if rows[n-1] != strings.Repeat("─", 60) || rows[n-3] != "" || rows[n-2] != " enter manage • escape/ctrl+c close" {
		t.Fatalf("tail = %q", rows[n-3:])
	}
	list := strings.Join(rows[5:n-3], "\n")
	for _, want := range []string{"alpha", "first", "beta", "second"} {
		if !strings.Contains(list, want) {
			t.Fatalf("list %q lacks %q", list, want)
		}
	}
	h.view.HandleInput("\x1b")
	if got := receive(t, answer, "the cancelled menu"); got.ok {
		t.Fatalf("Esc answered %v", got)
	}
	if h.renders.n.Load() == 0 {
		t.Fatal("the view never asked for a render")
	}
}

// ui.ts:117-141: Enter resolves the selected value, Down moves the selection first.
func TestMcpManagerViewMenuSelectsWithTheKeys(t *testing.T) {
	h := newManagerHarness(t)
	answer := h.menu(t.Context(), abMenu, nil)
	h.waitForText(t, "alpha")
	h.view.HandleInput("\r")
	if got := receive(t, answer, "Enter"); got != (menuAnswer{"a", true}) {
		t.Fatalf("Enter answered %v", got)
	}
	h = newManagerHarness(t)
	answer = h.menu(t.Context(), abMenu, nil)
	h.waitForText(t, "alpha")
	h.view.HandleInput("\x1b[B")
	h.view.HandleInput("\r")
	if got := receive(t, answer, "Down, Enter"); got != (menuAnswer{"b", true}) {
		t.Fatalf("Down, Enter answered %v", got)
	}
}

// ui.ts:114,127-133: `selected` is where the menu opens.
func TestMcpManagerViewMenuOpensOnTheSelectedItem(t *testing.T) {
	h := newManagerHarness(t)
	build := func() mcpext.McpMenu { menu := abMenu(); menu.Selected = "b"; return menu }
	answer := h.menu(t.Context(), build, nil)
	h.waitForText(t, "beta")
	h.view.HandleInput("\r")
	if got := receive(t, answer, "Enter"); got != (menuAnswer{"b", true}) {
		t.Fatalf("answered %v", got)
	}
}

// ui.ts:119-126: an empty menu shows its `empty` text and only the cancel hint; Enter does nothing, Esc cancels.
func TestMcpManagerViewEmptyMenu(t *testing.T) {
	h := newManagerHarness(t)
	answer := h.menu(t.Context(), func() mcpext.McpMenu {
		return mcpext.McpMenu{Title: "Servers", Empty: "Nothing configured.", ConfirmLabel: "manage", CancelLabel: "close"}
	}, nil)
	h.waitForText(t, "Nothing configured.")
	rows := h.rows(60)
	if rows[len(rows)-2] != " escape/ctrl+c close" {
		t.Fatalf("footer = %q", rows[len(rows)-2])
	}
	h.view.HandleInput("\r")
	select {
	case got := <-answer:
		t.Fatalf("Enter answered %v", got)
	case <-time.After(50 * time.Millisecond):
	}
	h.view.HandleInput("\x1b")
	if got := receive(t, answer, "Esc"); got.ok {
		t.Fatalf("Esc answered %v", got)
	}
	// An empty menu without text says so.
	answer = h.menu(t.Context(), func() mcpext.McpMenu { return mcpext.McpMenu{Title: "T"} }, nil)
	h.waitForText(t, "Nothing to show.")
	h.view.HandleInput("\x1b")
	receive(t, answer, "Esc")
}

// ui.ts:142-146: a change rebuilds the menu and keeps the selected item, wherever it moved to; the subscription ends with the menu.
func TestMcpManagerViewMenuRebuildsOnChangeKeepingTheSelection(t *testing.T) {
	h := newManagerHarness(t)
	var mu sync.Mutex
	items := []tui.SelectItem{{Value: "a", Label: "alpha"}, {Value: "b", Label: "beta"}}
	var listener func()
	var unsubscribed atomic.Bool
	build := func() mcpext.McpMenu {
		mu.Lock()
		defer mu.Unlock()
		return mcpext.McpMenu{Title: "Pick", Items: slices.Clone(items), ConfirmLabel: "ok", CancelLabel: "back"}
	}
	subscribe := func(l func()) func() {
		mu.Lock()
		listener = l
		mu.Unlock()
		return func() { unsubscribed.Store(true) }
	}
	answer := h.menu(t.Context(), build, subscribe)
	h.waitForText(t, "beta")
	h.view.HandleInput("\x1b[B")
	mu.Lock()
	items = []tui.SelectItem{{Value: "z", Label: "zeta"}, {Value: "a", Label: "alpha"}, {Value: "b", Label: "beta"}}
	notify := listener
	mu.Unlock()
	notify()
	h.waitForText(t, "zeta")
	h.view.HandleInput("\r")
	if got := receive(t, answer, "Enter"); got != (menuAnswer{"b", true}) {
		t.Fatalf("answered %v", got)
	}
	if !unsubscribed.Load() {
		t.Fatal("the menu did not end its subscription")
	}
	// A change after the menu ended does not draw it again.
	before := h.text(t)
	notify()
	if h.text(t) != before {
		t.Fatal("a finished menu was rebuilt")
	}
}

// ui.ts:149-151: status replaces the content with the title and a muted message; input does nothing there.
func TestMcpManagerViewStatus(t *testing.T) {
	h := newManagerHarness(t)
	h.view.Status("MCP server docs", "Reconnecting…")
	want := []string{strings.Repeat("─", 40), " MCP server docs", "", " Reconnecting…", strings.Repeat("─", 40)}
	if rows := h.rows(40); !slices.Equal(rows, want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
	h.view.HandleInput("\x1b")
	h.view.HandleInput("x")
	if rows := h.rows(40); !slices.Equal(rows, want) {
		t.Fatalf("rows after input = %q", rows)
	}
}

// ui.ts:153-208: the sign-in screen shows the URL and takes a pasted redirect URL.
func TestMcpManagerViewRedirectURL(t *testing.T) {
	h := newManagerHarness(t)
	answer := make(chan menuAnswer, 1)
	go func() {
		value, ok := h.view.RedirectURL(t.Context(), "Sign in to docs", "https://auth.example/authorize?x=1")
		answer <- menuAnswer{value, ok}
	}()
	h.waitForText(t, "https://auth.example/authorize?x=1")
	text := h.text(t)
	for _, want := range []string{" Sign in to docs", " Approve access in your browser. If it did not open, visit:",
		" If the browser runs on another machine, paste the URL it was redirected to:", " enter submit • escape/ctrl+c cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("view lacks %q:\n%s", want, text)
		}
	}
	// A blank value is not submitted.
	h.type_("  ")
	h.view.HandleInput("\r")
	select {
	case got := <-answer:
		t.Fatalf("a blank value answered %v", got)
	case <-time.After(50 * time.Millisecond):
	}
	h.type_("http://127.0.0.1:1/cb?code=9 ")
	h.view.HandleInput("\r")
	if got := receive(t, answer, "the redirect URL"); got != (menuAnswer{"http://127.0.0.1:1/cb?code=9", true}) {
		t.Fatalf("answered %v", got)
	}
}

// ui.ts (0.99.2, #10186): the sign-in URL is a terminal hyperlink, so it stays clickable when it wraps across lines, and
// a dim line names the click like /login does: Cmd+click on macOS, else Ctrl+click.
func TestMcpManagerViewRedirectURLShowsTheURLAsAHyperlinkWithAClickHint(t *testing.T) {
	h := newManagerHarness(t)
	const authorizationURL = "https://auth.example/authorize?x=1"
	go func() { _, _ = h.view.RedirectURL(t.Context(), "Sign in to docs", authorizationURL) }()
	h.waitForText(t, authorizationURL)
	hint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		hint = "Cmd+click to open"
	}
	var raw []string
	for _, row := range h.view.Render(80) {
		raw = append(raw, stripSGR(row))
	}
	text := strings.Join(raw, "\n")
	for _, want := range []string{tui.Hyperlink(authorizationURL, authorizationURL), tui.Hyperlink(hint, authorizationURL)} {
		if !strings.Contains(text, want) {
			t.Errorf("view lacks the hyperlink %q:\n%q", want, text)
		}
	}
	// A narrow terminal wraps the URL; the hyperlink must not break the frame's width.
	for _, row := range h.view.Render(20) {
		if w := widthx.VisibleWidth(row); w > 20 {
			t.Errorf("row %q is %d columns wide", row, w)
		}
	}
}

// ui.ts:164-172,193-199: Esc cancels, and so does the abort signal (the browser reached the callback), also before the screen shows.
func TestMcpManagerViewRedirectURLEnds(t *testing.T) {
	h := newManagerHarness(t)
	answer := make(chan menuAnswer, 1)
	go func() {
		value, ok := h.view.RedirectURL(t.Context(), "Sign in", "https://a")
		answer <- menuAnswer{value, ok}
	}()
	h.waitForText(t, "https://a")
	h.view.HandleInput("\x1b")
	if got := receive(t, answer, "Esc"); got.ok {
		t.Fatalf("Esc answered %v", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		value, ok := h.view.RedirectURL(ctx, "Sign in", "https://b")
		answer <- menuAnswer{value, ok}
	}()
	h.waitForText(t, "https://b")
	cancel()
	if got := receive(t, answer, "the abort"); got.ok {
		t.Fatalf("the abort answered %v", got)
	}
	aborted, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	if value, ok := h.view.RedirectURL(aborted, "Sign in", "https://c"); ok || value != "" {
		t.Fatalf("an aborted context answered %q, %v", value, ok)
	}
}

// ui.ts:88-97: focus goes to the input on the sign-in screen, and stays with the view when the content changes.
func TestMcpManagerViewFocusReachesTheInput(t *testing.T) {
	h := newManagerHarness(t)
	h.view.SetFocused(true)
	go func() { _, _ = h.view.RedirectURL(t.Context(), "Sign in", "https://a") }()
	h.waitForText(t, "https://a")
	if !strings.Contains(strings.Join(h.view.Render(80), "\n"), widthx.CursorMarker) {
		t.Fatal("the focused input draws no cursor")
	}
	h.view.SetFocused(false)
	if strings.Contains(strings.Join(h.view.Render(80), "\n"), widthx.CursorMarker) {
		t.Fatal("the unfocused input draws a cursor")
	}
}

// ui.ts:215-219: rows wider than the width are cut to it.
func TestMcpManagerViewCutsRowsToTheWidth(t *testing.T) {
	h := newManagerHarness(t)
	h.view.Status("A very long title that does not fit", "and a message that is longer than the width allows")
	for _, row := range h.view.Render(20) {
		if w := widthx.VisibleWidth(row); w > 20 {
			t.Fatalf("row %q is %d wide", row, w)
		}
	}
}

// scriptedUI answers the manager's menus in order and records what it was shown.
type scriptedUI struct {
	mu      sync.Mutex
	answers []string
	menus   []mcpext.McpMenu
	// subscribed records, per menu, whether the manager passed a subscription that rebuilds it.
	subscribed []bool
	statuses   [][2]string
}

func (u *scriptedUI) Menu(_ context.Context, build func() mcpext.McpMenu, subscribe func(func()) func()) (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.menus = append(u.menus, build())
	u.subscribed = append(u.subscribed, subscribe != nil)
	if len(u.answers) == 0 {
		return "", false
	}
	answer := u.answers[0]
	u.answers = u.answers[1:]
	return answer, answer != ""
}
func (u *scriptedUI) Status(title, message string) {
	u.mu.Lock()
	u.statuses = append(u.statuses, [2]string{title, message})
	u.mu.Unlock()
}
func (u *scriptedUI) RedirectURL(context.Context, string, string) (string, bool) { return "", false }

func itemsOf(menu mcpext.McpMenu) []string {
	var out []string
	for _, item := range menu.Items {
		out = append(out, item.Value+"|"+item.Label+"|"+item.Description)
	}
	return out
}

// index.ts:524-544,530-536: the servers menu, sorted by what needs the user first, then by name.
func TestMcpManagerServersMenu(t *testing.T) {
	h := newCommandHarness(t, "zulu", "alpha")
	ui := &scriptedUI{}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	menu := ui.menus[0]
	if menu.Title != "MCP servers" || menu.ConfirmLabel != "manage" || menu.CancelLabel != "close" || menu.Error != "" {
		t.Fatalf("menu = %+v", menu)
	}
	want := []string{"alpha|alpha|connected · 3 tools · codemode · test", "zulu|zulu|connected · 3 tools · codemode · test"}
	if got := itemsOf(menu); !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
	if menu.Empty != "No MCP servers configured. Add them to /agent/mcp.json or .pi/mcp.json." {
		t.Fatalf("empty = %q", menu.Empty)
	}
}

// index.ts:546-597,687-701: choosing a server opens its menu; disabling it shows the disconnecting status and the menu then offers Enable.
func TestMcpManagerServerMenuAndDisable(t *testing.T) {
	h := newCommandHarness(t, "docs")
	ui := &scriptedUI{answers: []string{"docs", "disable", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	if len(ui.menus) != 4 {
		t.Fatalf("%d menus shown", len(ui.menus))
	}
	server := ui.menus[1]
	if server.Title != "MCP server docs" || server.ConfirmLabel != "select" || server.CancelLabel != "back" {
		t.Fatalf("server menu = %+v", server)
	}
	if server.Details != "http://unused.invalid\nconfig: test\nState: connected · 3 tools" {
		t.Fatalf("details = %q", server.Details)
	}
	want := []string{"tools|Tools|3 offered", "reconnect|Reconnect|", "signout|Sign out|deletes the stored credentials",
		"exposure|Exposure|codemode", "disable|Disable|saved to mcp.json"}
	if got := itemsOf(server); !slices.Equal(got, want) || server.Selected != "tools" {
		t.Fatalf("items = %q selected %q", got, server.Selected)
	}
	if !slices.Equal(ui.statuses, [][2]string{{"MCP server docs", "Disconnecting…"}}) {
		t.Fatalf("statuses = %q", ui.statuses)
	}
	disabled := false
	if len(h.updates) != 1 || h.updates[0].Enabled == nil || *h.updates[0].Enabled != disabled {
		t.Fatalf("saved %+v", h.updates)
	}
	if got := itemsOf(ui.menus[2]); !slices.Equal(got, []string{"enable|Enable|saved to mcp.json"}) {
		t.Fatalf("disabled menu = %q", got)
	}
}

// index.ts:599-618,660-661: the tools of a server, with the exposure it overrides.
func TestMcpManagerToolsMenu(t *testing.T) {
	h := newCommandHarness(t, "docs")
	ui := &scriptedUI{answers: []string{"docs", "tools", "", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	tools := ui.menus[2]
	if tools.Title != "Tools of docs" || tools.ConfirmLabel != "back" || tools.CancelLabel != "back" || tools.Empty != "The server offers no tools." {
		t.Fatalf("tools menu = %+v", tools)
	}
	if tools.Details != "Exposure codemode: called from codemode scripts, which find them with searchTools()" {
		t.Fatalf("details = %q", tools.Details)
	}
	want := []string{"search|search|Search the docs.", "fail|fail|Always fails.", "shot|shot|Returns an image."}
	if got := itemsOf(tools); !slices.Equal(got, want) {
		t.Fatalf("items = %q", got)
	}
}

// index.ts:620-641: the exposure menu marks the current one and saves a different choice.
func TestMcpManagerExposureMenu(t *testing.T) {
	h := newCommandHarness(t, "docs")
	ui := &scriptedUI{answers: []string{"docs", "exposure", "direct", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	menu := ui.menus[2]
	if menu.Title != "Exposure of docs" || menu.Details != "Saved to test." || menu.Selected != "codemode" || menu.ConfirmLabel != "save" || menu.CancelLabel != "back" {
		t.Fatalf("exposure menu = %+v", menu)
	}
	want := []string{
		"codemode|✓ codemode|called from codemode scripts, which find them with searchTools()",
		"deferred|  deferred|not declared until tool_search loads them, then called directly; no codemode needed",
		"direct|  direct|declared to the model like built-in tools",
	}
	if got := itemsOf(menu); !slices.Equal(got, want) {
		t.Fatalf("items = %q", got)
	}
	if len(h.updates) != 1 || h.updates[0].Exposure != extension.McpExposure("direct") {
		t.Fatalf("saved %+v", h.updates)
	}
	// The server menu that follows shows the new exposure.
	if got := itemsOf(ui.menus[3]); !slices.Contains(got, "exposure|Exposure|direct") {
		t.Fatalf("server menu after = %q", got)
	}
}

// index.ts:663-670: reconnecting shows its status; the state after it is the menu's.
func TestMcpManagerReconnectShowsItsStatus(t *testing.T) {
	h := newCommandHarness(t, "docs")
	ui := &scriptedUI{answers: []string{"docs", "reconnect", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ui.statuses, [][2]string{{"MCP server docs", "Reconnecting…"}}) {
		t.Fatalf("statuses = %q", ui.statuses)
	}
	if got := ui.menus[2].Details; !strings.HasSuffix(got, "State: connected · 3 tools") {
		t.Fatalf("details after = %q", got)
	}
}

// index.ts:695-699: a server that is no longer configured ends its menu loop.
func TestMcpManagerServerMenuOfAMissingServer(t *testing.T) {
	h := newCommandHarness(t, "docs")
	ui := &scriptedUI{answers: []string{"gone", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	if m := ui.menus[1]; m.Title != "gone" || m.Empty != "This server is no longer configured." || m.CancelLabel != "back" || m.ConfirmLabel != "" || len(m.Items) != 0 {
		t.Fatalf("menu = %+v", m)
	}
}

// index.ts:517-531: servers that need the user come first, and a failed server shows the first line of its error.
func TestMcpManagerServersMenuListsFailedServersFirst(t *testing.T) {
	h := newCommandHarness(t, "alpha", "fail-beta")
	ui := &scriptedUI{}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	want := []string{"fail-beta|fail-beta|failed: connection refused · codemode · test", "alpha|alpha|connected · 3 tools · codemode · test"}
	if got := itemsOf(ui.menus[0]); !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

// index.ts:591-596,682: the message of an action that failed shows in the server menu, and the connection error too.
func TestMcpManagerShowsTheMessageOfAFailedAction(t *testing.T) {
	h := newCommandHarness(t, "alpha", "fail-beta")
	h.saveError = errors.New("disk full")
	ui := &scriptedUI{answers: []string{"alpha", "exposure", "direct", "", "fail-beta", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	if got := ui.menus[3].Error; !strings.Contains(got, "disk full") {
		t.Fatalf("server menu error after a failed save = %q", got)
	}
	failed := ui.menus[5]
	if failed.Error != "connection refused\nsecond line" {
		t.Fatalf("failed server error = %q", failed.Error)
	}
	if got := itemsOf(failed); !slices.Equal(got, []string{"reconnect|Reconnect|", "exposure|Exposure|codemode", "disable|Disable|saved to mcp.json"}) {
		t.Fatalf("failed server items = %q", got)
	}
}

// index.ts:692-697,604,624: the servers and server menus rebuild on every change (ui.menu(..., subscribe)); the tools and
// exposure menus are built once (ui.menu(build) without subscribe).
func TestMcpManagerSubscribesOnlyTheServerMenus(t *testing.T) {
	h := newCommandHarness(t, "docs")
	ui := &scriptedUI{answers: []string{"docs", "tools", "", "exposure", "", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, menu := range ui.menus {
		titles = append(titles, menu.Title)
	}
	wantTitles := []string{"MCP servers", "MCP server docs", "Tools of docs", "MCP server docs", "Exposure of docs", "MCP server docs", "MCP servers"}
	if !slices.Equal(titles, wantTitles) {
		t.Fatalf("menus = %q, want %q", titles, wantTitles)
	}
	if want := []bool{true, true, false, true, false, true, true}; !slices.Equal(ui.subscribed, want) {
		t.Fatalf("subscribed = %v, want %v", ui.subscribed, want)
	}
}

// index.ts:665-666,687-701: a sign-out whose credential removal throws rejects runAction and so manage, which
// showMcpManager reports; no further menu is shown.
func TestMcpManagerSignOutFailureEndsTheManager(t *testing.T) {
	h := newCommandHarnessWithCredentials(t, failingCredentials{}, "docs")
	ui := &scriptedUI{answers: []string{"docs", "signout", "", ""}}
	if err := h.ext.Manage(t.Context(), ui, h.ctx.EventContext); !errors.Is(err, errCredentials) {
		t.Fatalf("Manage = %v, want %v", err, errCredentials)
	}
	if len(ui.menus) != 2 {
		t.Fatalf("%d menus shown after the failure, want 2", len(ui.menus))
	}
}
