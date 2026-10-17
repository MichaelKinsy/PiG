package mcpext_test

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/coding/mcpext/mcptui"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/tui"
)

type managerViewProbe struct {
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Details  string   `json:"details"`
	Error    string   `json:"error"`
	Empty    *string  `json:"empty"`
	Selected string   `json:"selected"`
	Confirm  string   `json:"confirm"`
	Cancel   string   `json:"cancel"`
	Items    []item   `json:"items"`
	Message  string   `json:"message"`
	Cancels  bool     `json:"cancels"`
	URL      string   `json:"url"`
	Keys     []string `json:"keys"`
	Widths   []int    `json:"widths"`
	// Bindings are user keybindings: the footer hints name the keys bound to select.confirm and select.cancel.
	Bindings map[string][]string `json:"bindings,omitempty"`
}

type item struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// The page keys are left out: SelectList pages the selection under D155 where Pi 1.1.0 ignores them.
// The /mcp manager view (mcp/ui.js) against Pi's component: menus (details, error, empty message, more items than fit, long and wide labels and descriptions, initial selection, arrow-key moves with wraparound),
// the status screen with and without a cancel hint, and the sign-in screen with typed input, rendered at several widths. Rows are compared byte for byte, colours included.
func TestMcpManagerViewMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(55))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	words := []string{"alpha", "beta", "日本語のサーバー", "😀 emoji", "a very long label that will not fit in a narrow terminal at all", "tab\there", "  padded  ", "x", "multi\nline"}
	var probes []managerViewProbe
	for range 120 {
		widths := []int{20, 40, 80, 120}
		keys := []string{}
		for range rng.Intn(5) {
			keys = append(keys, pick("\x1b[B", "\x1b[A", "\x1b[B", "j"))
		}
		switch rng.Intn(5) {
		case 0, 1, 2:
			p := managerViewProbe{Kind: "menu", Title: pick("MCP servers", "日本", "A title that is far longer than a narrow terminal can hold"), Confirm: pick("manage", "open"), Cancel: pick("close", "back"), Keys: keys, Widths: widths}
			if rng.Intn(2) == 0 {
				p.Details = pick("details", "multi\nline details", "日本語 details that are long enough to wrap around in a narrow terminal for sure")
			}
			if rng.Intn(3) == 0 {
				p.Error = pick("it broke", "line one\nline two")
			}
			count := pick("0", "1", "3", "12", "13", "30")
			n := 0
			_, _ = fmt.Sscan(count, &n)
			for i := range n {
				it := item{Value: fmt.Sprintf("v%d", i), Label: pick(words...)}
				if rng.Intn(2) == 0 {
					it.Description = pick(words...)
				}
				p.Items = append(p.Items, it)
			}
			if n == 0 && rng.Intn(2) == 0 {
				// Pi's `menu.empty ?? "Nothing to show."` keeps an empty message.
				empty := pick("Nothing here", "No servers configured", "")
				p.Empty = &empty
			}
			if n > 0 && rng.Intn(2) == 0 {
				p.Selected = pick("v0", fmt.Sprintf("v%d", n-1), "missing", fmt.Sprintf("v%d", rng.Intn(n)))
			}
			probes = append(probes, p)
		case 3:
			probes = append(probes, managerViewProbe{Kind: "status", Title: pick("Status", "日本"), Message: pick("Connecting…", "line one\nline two", "a long message that goes on and on and on past the end of a narrow terminal width"), Cancels: rng.Intn(2) == 0, Widths: widths})
		default:
			probes = append(probes, managerViewProbe{Kind: "redirect", Title: pick("Sign in", "日本"), URL: pick("https://auth.example.com/authorize?client_id=abc&redirect_uri=http%3A%2F%2Flocalhost%3A1234", "https://x.test/"), Keys: []string{pick("p", "pasted", "日本")}, Widths: widths})
		}
	}
	rebindings := []map[string][]string{
		{"tui.select.confirm": {"ctrl+s"}},
		{"tui.select.cancel": {"ctrl+q", "ctrl+x"}},
		{"tui.select.confirm": {}, "tui.select.cancel": {}},
		{"tui.select.down": {"ctrl+n"}, "tui.select.up": {"ctrl+p"}, "tui.select.cancel": {"q"}},
	}
	for i := range probes {
		if i%4 == 0 {
			probes[i].Bindings = rebindings[(i/4)%len(rebindings)]
			// A rebound select key is also pressed, so the matching path is compared with the hint.
			probes[i].Keys = append(probes[i].Keys, "\x13", "\x11", "\x0e", "q")
		}
	}
	previousBindings := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previousBindings) })
	var want [][][]string
	pioracle.Run(t, `
const ui = await load("pi-coding-agent/extensions/mcp/ui.js");
const themeModule = await load("pi-coding-agent/modes/interactive/theme/theme.js");
themeModule.initTheme("dark");
const kb = await load("pi-coding-agent/core/keybindings.js");
const tuiModule = await load("../pi-tui.mjs");
const tui = { requestRender() {}, terminal: { columns: 80, rows: 24 } };
const out = [];
for (const p of input) {
	const keybindings = new kb.KeybindingsManager(p.bindings ?? {});
	tuiModule.setKeybindings(keybindings);
	const view = new ui.McpManagerView(tui, themeModule.theme, keybindings);
	if (p.kind === "menu") {
		void view.menu(() => ({ title: p.title, details: p.details || undefined, error: p.error || undefined, items: (p.items ?? []).map((i) => ({ value: i.value, label: i.label, description: i.description })), empty: p.empty ?? undefined, selected: p.selected || undefined, confirmLabel: p.confirm, cancelLabel: p.cancel }));
	} else if (p.kind === "status") {
		view.status(p.title, p.message, p.cancels ? () => {} : undefined);
	} else {
		void view.redirectUrl(p.title, p.url, new AbortController().signal);
	}
	const rows = [];
	for (const w of p.widths) rows.push(view.render(w));
	for (const k of p.keys ?? []) { view.handleInput(k); const after = []; for (const w of p.widths) after.push(view.render(w)); rows.push(...after); }
	out.push(rows);
}
emit(out);
`, probes, &want, "COLORTERM=truecolor", "FORCE_COLOR=3")
	bad := 0
	for i, p := range probes {
		var got [][]string
		ctx, cancel := context.WithCancel(t.Context())
		// The interactive mode's merged tui and app table, which the manager view reads its keys from.
		userBindings := map[string][]codingagent.KeyID{}
		for action, keys := range p.Bindings {
			userBindings[action] = []codingagent.KeyID{}
			for _, key := range keys {
				userBindings[action] = append(userBindings[action], codingagent.KeyID(key))
			}
		}
		codingagent.NewKeybindingsManagerFromBindings(userBindings, "")
		bindings := tui.GetTUIKeybindings()
		view := mcptui.NewMcpManagerView(&renderCounter{}, coloredTheme(t), bindings)
		switch p.Kind {
		case "menu":
			items := make([]mcpext.McpMenuItem, len(p.Items))
			for j, it := range p.Items {
				items[j] = mcpext.McpMenuItem{Value: it.Value, Label: it.Label, Description: it.Description}
			}
			menu := mcpext.McpMenu{Title: p.Title, Details: p.Details, Error: p.Error, Items: items, Selected: p.Selected, ConfirmLabel: p.Confirm, CancelLabel: p.Cancel}
			if p.Empty != nil {
				menu.Empty, menu.HasEmpty = *p.Empty, true
			}
			go view.Menu(ctx, func() mcpext.McpMenu { return menu }, nil)
		case "status":
			var onCancel func()
			if p.Cancels {
				onCancel = func() {}
			}
			view.Status(p.Title, p.Message, onCancel)
		default:
			go view.RedirectURL(ctx, p.Title, p.URL)
		}
		deadline := time.Now().Add(5 * time.Second)
		for strings.Contains(strings.Join(view.Render(80), "\n"), "Loading…") && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		for _, w := range p.Widths {
			got = append(got, view.Render(w))
		}
		for _, k := range p.Keys {
			view.HandleInput(k)
			for _, w := range p.Widths {
				got = append(got, view.Render(w))
			}
		}
		cancel()
		if strings.Join(flatten(got), "\x00") != strings.Join(flatten(want[i]), "\x00") {
			if bad++; bad <= 4 {
				t.Errorf("probe %d kind=%s title=%q keys=%q items=%d: %s", i, p.Kind, p.Title, p.Keys, len(p.Items), firstRowDiff(got, want[i]))
			}
		}
	}
	if bad > 4 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}

func flatten(rows [][]string) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r...)
		out = append(out, "\x01")
	}
	return out
}

func firstRowDiff(got, want [][]string) string {
	for screen := 0; screen < max(len(got), len(want)); screen++ {
		var g, w []string
		if screen < len(got) {
			g = got[screen]
		}
		if screen < len(want) {
			w = want[screen]
		}
		for row := 0; row < max(len(g), len(w)); row++ {
			var a, b string
			if row < len(g) {
				a = g[row]
			}
			if row < len(w) {
				b = w[row]
			}
			if a != b {
				return fmt.Sprintf("screen %d row %d:\n  got  %q\n  want %q", screen, row, a, b)
			}
		}
	}
	return "equal"
}
