//go:build linux || darwin

package uiviewweb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/browsertest"
)

// keys are the keys press sends: the DOM code, the virtual key code and the text a key types.
var keys = map[string]struct {
	code string
	vk   int
	text string
}{
	"Enter": {"Enter", 13, "\r"}, " ": {"Space", 32, " "}, "Tab": {"Tab", 9, ""}, "Escape": {"Escape", 27, ""},
	"ArrowDown": {"ArrowDown", 40, ""}, "ArrowUp": {"ArrowUp", 38, ""}, "ArrowRight": {"ArrowRight", 39, ""}, "ArrowLeft": {"ArrowLeft", 37, ""},
}

// The modifier bits of a key event in the DevTools protocol.
const (
	modCtrl  = 2
	modShift = 8
)

// press sends one key to the focused element as the keyboard does. The event is trusted, so a native control acts on
// it: Enter and Space click a button, Space toggles a checkbox, Tab moves the focus.
func press(t *testing.T, browser *browsertest.Browser, key string, modifiers int) {
	t.Helper()
	k, ok := keys[key]
	if !ok && len(key) == 1 {
		// A character key: it types itself.
		k.code, k.vk, k.text = "Key"+strings.ToUpper(key), int(strings.ToUpper(key)[0]), key
	} else if !ok {
		t.Fatalf("press: no key %q", key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, kind := range []string{"keyDown", "keyUp"} {
		params := map[string]any{"type": kind, "key": key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": modifiers}
		if kind == "keyDown" && k.text != "" && modifiers == 0 {
			params["text"] = k.text
		}
		if _, err := browser.Call(ctx, "Input.dispatchKeyEvent", params); err != nil {
			t.Fatal(err)
		}
	}
}

// recordFrames makes the page keep the body of every ui_view_intent command it posts, in window.frames.
const recordFrames = `(() => { window.frames = []; const post = window.fetch; window.fetch = (url, init) => {
	if (typeof init?.body === "string" && init.body.includes('"type":"ui_view_intent"')) window.frames.push(init.body);
	return post(url, init); }; })()`

// TestPageIntentFramesAreTheTextClientsFrames drives every interactive node kind of the demo panel with the keyboard
// and the pointer and compares the ui_view_intent commands the page posts, byte for byte, with the ones the page
// posted for the same keys and clicks before it drew controls (testdata/intent-frames.txt, taken from the client of
// commit 1a43bce90c). What a control looks like changed; what it tells the extension did not.
func TestPageIntentFramesAreTheTextClientsFrames(t *testing.T) {
	want, err := os.ReadFile("testdata/intent-frames.txt")
	if err != nil {
		t.Fatal(err)
	}
	b := startBridge(t)
	browser := browsertest.Start(t)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	browser.Run(recordFrames)
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.focus(); form.elements.message.value = "/ui-demo"; form.requestSubmit(); })()`)
	browser.Wait(`document.querySelector('[data-key="demo"]') !== null && document.activeElement.id.endsWith("-colors")`)
	sent := 0
	// settled waits until the page posted one more intent and shows what its handler drew or answered.
	settled := func(shows string) {
		t.Helper()
		sent++
		browser.Wait(`window.frames.length === ` + strconv.Itoa(sent) + ` && (` + shows + `)`)
	}
	greeting := func(part string) string {
		return `document.querySelector('[data-key="demo"] .text').textContent.includes("` + part + `")`
	}
	focus := func(id string) {
		t.Helper()
		browser.Run(`document.querySelector('[id$="-` + id + `"]').focus()`)
		browser.Wait(`document.activeElement.id.endsWith("-` + id + `")`)
	}
	// select-list: the arrow key and Enter, then a click on an item.
	press(t, browser, "ArrowDown", 0)
	press(t, browser, "Enter", 0)
	settled(greeting("green"))
	browser.Run(`document.querySelectorAll('[id$="-colors"] [role=option]')[2].click()`)
	settled(greeting("blue"))
	// mouse-region: Enter, Space, a click.
	focus("tap")
	press(t, browser, "Enter", 0)
	settled(greeting(", 1 taps"))
	press(t, browser, " ", 0)
	settled(greeting(", 2 taps"))
	browser.Run(`document.querySelector('[id$="-tap"]').click()`)
	settled(greeting(", 3 taps"))
	// input: text and Enter.
	focus("name")
	browser.Type("Ada")
	press(t, browser, "Enter", 0)
	settled(greeting("Hello Ada"))
	// settings-list: Enter steps the value, a click steps it again.
	focus("prefs")
	press(t, browser, "Enter", 0)
	settled(greeting("wrap off"))
	browser.Run(`document.querySelector('[id$="-prefs"] [role=option]').click()`)
	settled(greeting("wrap on"))
	// editor: text and Ctrl+Enter.
	focus("notes")
	browser.Type("a note")
	press(t, browser, "Enter", modCtrl)
	settled(`document.querySelector('[data-key="demo"] footer').textContent.startsWith("demo.note: done")`)
	// An intent of the mount itself: its button.
	browser.Run(`[...document.querySelectorAll('[data-key="demo"] header button')].find((b) => b.dataset.intent === "demo.close" || b.textContent === "demo.close").click()`)
	settled(`document.querySelector('[data-key="demo"]') === null`)
	got := browser.String(`window.frames.join("\n") + "\n"`)
	if got != string(want) {
		t.Errorf("the page posted\n%s\nthe text client posted\n%s", got, want)
	}
	if n := strings.Count(got, "\n"); n != 10 {
		t.Errorf("the page posted %d intents, want 10", n)
	}
}

// mouse sends one mouse event at a point of the page, as the pointer does: the browser finds what is under it.
func mouse(t *testing.T, browser *browsertest.Browser, kind string, x, y float64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	params := map[string]any{"type": kind, "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1}
	if kind == "mouseReleased" {
		params["buttons"] = 0
	}
	if _, err := browser.Call(ctx, "Input.dispatchMouseEvent", params); err != nil {
		t.Fatal(err)
	}
}

// accessible lists what the browser computed for assistive technology: one line for every element whose role is one
// of the roles, sorted, with its accessible name and the states it has.
func accessible(t *testing.T, browser *browsertest.Browser, roles ...string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := browser.Call(ctx, "Accessibility.enable", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := browser.Call(ctx, "Accessibility.getFullAXTree", nil)
	if err != nil {
		t.Fatal(err)
	}
	var tree struct {
		Nodes []struct {
			Ignored bool `json:"ignored"`
			Role    struct {
				Value string `json:"value"`
			} `json:"role"`
			Name struct {
				Value string `json:"value"`
			} `json:"name"`
			Properties []struct {
				Name  string `json:"name"`
				Value struct {
					Value any `json:"value"`
				} `json:"value"`
			} `json:"properties"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, n := range tree.Nodes {
		if n.Ignored || !strings.Contains(" "+strings.Join(roles, " ")+" ", " "+n.Role.Value+" ") {
			continue
		}
		line := n.Role.Value + " " + strconv.Quote(n.Name.Value)
		for _, state := range []string{"level", "checked", "selected", "disabled", "required"} {
			for _, p := range n.Properties {
				if p.Name == state && (state != "required" || p.Value.Value != false) {
					line += fmt.Sprintf(" %s=%v", state, p.Value.Value)
				}
			}
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
}

// controls is the content of a panel that uses every control the page draws: each node kind, and for the kinds that
// are what their a11y role says, each role. The state is what an update changes.
func controls(title string, selected int, visited, live bool, progress int) string {
	region := func(id, label string) string {
		return `{"kind":"mouse-region","id":"` + id + `","region":{"types":["press"]},"children":[{"kind":"text","text":"` + label + `"}]}`
	}
	value := `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`
	intent := func(name, node, arg string) string {
		return `{"name":"fx.` + name + `","nodeKey":"` + node + `","arg":` + arg + `,"timeoutMs":100}`
	}
	return `{"view":{"root":{"kind":"vstack","gap":1,"children":[` +
		`{"kind":"text","id":"title","text":"` + title + `","paddingX":0,"paddingY":0},` +
		`{"kind":"box","children":[{"kind":"text","id":"sub","text":"Nearby","paddingX":0,"paddingY":0}]},` +
		`{"kind":"input","id":"q","value":"","placeholder":"Search"},` +
		`{"kind":"select-list","id":"results","items":[{"value":"a","label":"Harbour","description":"1.2 km"},{"value":"b","label":"Museum","description":"2.0 km"},{"value":"c","label":"Station"}],"selectedIndex":` + strconv.Itoa(selected) + `},` +
		`{"kind":"select-list","id":"modes","items":[{"value":"walk","label":"Walk"},{"value":"bike","label":"Bike"}],"selectedIndex":0},` +
		`{"kind":"settings-list","id":"prefs","items":[{"id":"units","label":"Units","currentValue":"km","values":["km","mi"]}]},` +
		`{"kind":"hstack","gap":1,"children":[` + region("go", "Route") + `,` + region("plain", "Plain") + `,` + region("off", "Offline") + `]},` +
		region("done", "Visited") + `,` + region("live", "Live traffic") + `,` + region("fast", "Fastest") + `,` +
		`{"kind":"lines","id":"prog","content":["` + strconv.Itoa(progress) + ` of 4"],"progress":{"value":` + strconv.Itoa(progress) + `,"max":4}},` +
		`{"kind":"lines","id":"stops","content":["Harbour 1.2 km 4 min","Museum 2.0 km 7 min"],"list":{"items":[{"label":"Harbour","detail":"1.2 km","columns":["4 min"]},{"label":"Museum","detail":"2.0 km","columns":["7 min"]}],"selectedIndex":` + strconv.Itoa(selected%2) + `}},` +
		`{"kind":"lines","id":"coords","content":["48.8566, 2.3522"]},` +
		`{"kind":"editor","id":"notes","value":""},` +
		`{"kind":"scroll-view","id":"log","children":[{"kind":"text","text":"a line of the log"}]},` +
		`{"kind":"dynamic-border"},{"kind":"loader","message":"Searching"},{"kind":"markdown","text":"Some ` + "`code`" + ` here"}]}},` +
		`"intents":[` + intent("search", "q", value) + `,` + intent("pick", "results", value) + `,` + intent("mode", "modes", value) + `,` +
		intent("pref", "prefs", `{"type":"object","properties":{"item":{"type":"string"},"value":{"type":"string"}},"required":["item","value"]}`) + `,` +
		intent("go", "go", `{"type":"object"}`) + `,` + intent("plain", "plain", `{"type":"object"}`) + `,` + intent("off", "off", `{"type":"object"}`) + `,` +
		intent("visit", "done", `{"type":"object"}`) + `,` + intent("live", "live", `{"type":"object"}`) + `,` + intent("fast", "fast", `{"type":"object"}`) + `,` +
		intent("note", "notes", value) + `,{"name":"fx.zoomIn","arg":{"type":"object"},"timeoutMs":100}],` +
		`"a11y":[{"node":"title","role":"heading"},{"node":"sub","role":"heading"},{"node":"q","name":"Search places","required":true},` +
		`{"node":"results","name":"Results"},{"node":"modes","name":"Travel mode","role":"tablist"},{"node":"prefs","name":"Preferences"},` +
		`{"node":"go","name":"Route","role":"button"},{"node":"off","name":"Offline","role":"button","disabled":true},` +
		`{"node":"done","name":"Visited","role":"checkbox","checked":` + strconv.FormatBool(visited) + `},{"node":"live","name":"Live traffic","role":"switch","checked":` + strconv.FormatBool(live) + `},` +
		`{"node":"fast","name":"Fastest","role":"radio","checked":true},{"node":"prog","name":"Downloaded"},{"node":"stops","name":"Stops"},` +
		`{"node":"notes","name":"Notes"},{"node":"log","name":"Log","role":"region"}],` +
		`"focus":[{"node":"go"},{"node":"q"},{"node":"results"},{"node":"done"}]}`
}

// at is the element of the panel fx-a that carries a node id.
func at(id string) string {
	return `document.querySelector('[data-key="fx-a"] [data-node="` + id + `"]')`
}

// TestPageDrawsNodesAsControls: every node kind is the control it is, by its element, and to assistive technology by
// the role, the name and the state the browser computes for it. Nothing on the page is a control drawn with
// characters, and only rows and code are monospace.
func TestPageDrawsNodesAsControls(t *testing.T) {
	browser, send := openFixtures(t)
	send(`/fx-a mount ` + controls("Places", 1, true, false, 3))
	browser.Wait(at("log") + ` !== null`)
	got := browser.String(`JSON.stringify(["title","sub","q","results","modes","prefs","go","plain","off","done","live","fast","prog","stops","coords","notes","log"].map((id) => { const el = ` + at(`' + id + '`) + `;
		return [id, el.tagName, el.getAttribute("type") ?? "", el.getAttribute("role") ?? "", ["checkbox", "radio"].includes(el.type) ? el.checked : "", el.disabled === true ? "disabled" : ""].join(" ").trimEnd(); }))`)
	want := `["title H2","sub H3","q INPUT text","results DIV  listbox","modes DIV  tablist","prefs DIV  listbox","go BUTTON button","plain BUTTON button","off BUTTON button   disabled",` +
		`"done INPUT checkbox  true","live INPUT checkbox switch false","fast INPUT radio  true","prog PROGRESS","stops DIV  list","coords PRE","notes TEXTAREA","log DIV  region"]`
	if got != want {
		t.Errorf("the nodes are drawn as\n  %s\nwant\n  %s", got, want)
	}
	got = browser.String(`(() => { const q = (s) => document.querySelector('[data-key="fx-a"] ' + s), all = (s) => [...document.querySelectorAll('[data-key="fx-a"] ' + s)];
		const mono = (el) => /mono/i.test(getComputedStyle(el).fontFamily);
		return JSON.stringify({
			options: all('[data-node="results"] [role=option]').map((o) => o.getAttribute("aria-selected")).join(),
			tabs: all('[data-node="modes"] [role=tab]').map((o) => o.textContent + "=" + o.getAttribute("aria-selected")).join(),
			progress: q("progress").value + "/" + q("progress").max,
			rows: all('[data-node="stops"] [role=listitem]').map((r) => [...r.children].map((c) => c.textContent).join("|") + (r.getAttribute("aria-current") === "true" ? "*" : "")).join(),
			columns: getComputedStyle(q('[data-node="stops"]')).gridTemplateColumns.split(" ").length,
			label: q("label.check").textContent + "/" + (q("label.check").control === q('[data-node="done"]')),
			loader: q(".loader").getAttribute("role") + "/" + q(".loader").textContent,
			rule: q("hr") !== null,
			action: all("header button").map((b) => b.textContent + "=" + b.dataset.intent).join(),
			mono: [mono(q('[data-node="title"]')), mono(q('[data-node="go"]')), mono(q("[role=option]")), mono(q('[data-node="q"]')), mono(q('[data-node="coords"]')), mono(q(".markdown code"))].join(),
		}); })()`)
	want = `{"options":"false,true,false","tabs":"Walk=true,Bike=false","progress":"3/4","rows":"Harbour|1.2 km|4 min,Museum|2.0 km|7 min*","columns":3,"label":"Visited/true",` +
		`"loader":"status/Searching","rule":true,"action":"Zoom in=fx.zoomIn","mono":"false,false,false,false,true,true"}`
	if got != want {
		t.Errorf("the controls show\n  %s\nwant\n  %s", got, want)
	}
	// No text on the page draws a control with characters.
	if got := browser.String(`(() => { const bad = []; const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
		for (let n; (n = walk.nextNode()); ) if (/\[[xX ]\]|[\u2500-\u259f]/.test(n.data)) bad.push(n.data); return JSON.stringify(bad); })()`); got != "[]" {
		t.Errorf("text that draws a control or a box with characters: %s", got)
	}
	lines := accessible(t, browser, "heading", "textbox", "listbox", "option", "tablist", "tab", "button", "checkbox", "switch", "radio", "progressbar", "list", "listitem", "status", "region")
	var mine []string
	for _, line := range lines {
		// The page's own prompt and status line are not the view's.
		if line != `textbox "Message"` && line != `button "Send"` && line != `button "Side by side"` && !strings.HasPrefix(line, `tab "fx-a"`) && line != `tablist "Panels"` {
			mine = append(mine, line)
		}
	}
	// The two status lines are the loader and the mount's; the page's own is empty and not drawn.
	wantTree := []string{
		`button "Offline" disabled=true`, `button "Plain"`, `button "Route"`, `button "Zoom in"`, `checkbox "Visited" checked=true`,
		`heading "Nearby" level=3`, `heading "Places" level=2`, `list "Stops"`, `listbox "Preferences"`, `listbox "Results"`, `listitem "" level=1`, `listitem "" level=1`,
		`option "Harbour 1.2 km" selected=false`, `option "Museum 2.0 km" selected=true`, `option "Station" selected=false`, `option "Units km" selected=true`,
		`progressbar "Downloaded"`, `radio "Fastest" checked=true`, `region "Log"`, `status ""`, `status ""`, `switch "Live traffic" checked=false`,
		`tab "Bike" selected=false`, `tab "Walk" selected=true`, `tablist "Travel mode"`, `textbox "Notes"`, `textbox "Search places" required=true`,
	}
	if strings.Join(mine, "\n") != strings.Join(wantTree, "\n") {
		t.Errorf("assistive technology is told\n%s\nwant\n%s", strings.Join(mine, "\n"), strings.Join(wantTree, "\n"))
	}
}

// TestPageControlsRaiseTheirIntents: a click and the keyboard on each control raise the intent of its node with the
// argument the node gives, a disabled control raises none, a checkbox shows the view's state again once its intent
// settled without an update, and the Tab key walks the nodes in the order of the focus records.
func TestPageControlsRaiseTheirIntents(t *testing.T) {
	browser, send := openFixtures(t)
	send(`/fx-a mount ` + controls("Places", 1, true, false, 3))
	browser.Wait(at("log") + ` !== null`)
	browser.Run(recordFrames)
	var want []string
	raised := func(intent, node, arg string) {
		t.Helper()
		want = append(want, intent+" "+node+" "+arg)
		browser.Wait(`window.frames.length === ` + strconv.Itoa(len(want)))
	}
	focus := func(id string) {
		t.Helper()
		browser.Run(at(id) + `.focus()`)
		browser.Wait(`document.activeElement === ` + at(id))
	}
	click := func(id string) {
		t.Helper()
		browser.Run(at(id) + `.click()`)
	}
	focus("done")
	press(t, browser, " ", 0)
	raised("fx.visit", "done", "{}")
	click("done")
	raised("fx.visit", "done", "{}")
	focus("live")
	press(t, browser, " ", 0)
	raised("fx.live", "live", "{}")
	click("fast")
	raised("fx.fast", "fast", "{}")
	focus("plain")
	press(t, browser, "Enter", 0)
	raised("fx.plain", "plain", "{}")
	press(t, browser, " ", 0)
	raised("fx.plain", "plain", "{}")
	click("go")
	raised("fx.go", "go", "{}")
	click("off") // disabled: nothing
	focus("modes")
	press(t, browser, "ArrowRight", 0)
	press(t, browser, "Enter", 0)
	raised("fx.mode", "modes", `{"value":"bike"}`)
	browser.Run(at("modes") + `.firstChild.click()`)
	raised("fx.mode", "modes", `{"value":"walk"}`)
	focus("results")
	press(t, browser, "ArrowDown", 0)
	press(t, browser, "Enter", 0)
	raised("fx.pick", "results", `{"value":"c"}`)
	focus("prefs")
	press(t, browser, "Enter", 0)
	raised("fx.pref", "prefs", `{"item":"units","value":"mi"}`)
	focus("q")
	browser.Type("quay")
	press(t, browser, "Enter", 0)
	raised("fx.search", "q", `{"value":"quay"}`)
	focus("notes")
	browser.Type("by the water")
	press(t, browser, "Enter", modCtrl)
	raised("fx.note", "notes", `{"value":"by the water"}`)
	browser.Run(`document.querySelector('[data-key="fx-a"] header button').click()`)
	raised("fx.zoomIn", "", "{}")
	var frames []struct {
		Intent  string          `json:"intent"`
		NodeKey string          `json:"nodeKey"`
		Arg     json.RawMessage `json:"arg"`
	}
	if err := json.Unmarshal([]byte(browser.String(`"[" + window.frames.join(",") + "]"`)), &frames); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range frames {
		got = append(got, f.Intent+" "+f.NodeKey+" "+string(f.Arg))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the controls raised\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// No handler redrew the view: the checkboxes the user toggled show what the view says.
	browser.Wait(`document.querySelector('[data-key="fx-a"] footer').dataset.outcome !== undefined && window.uiView.reports === 0`)
	browser.Wait(at("done") + `.checked === true && ` + at("live") + `.checked === false`)
	// Tab walks the focus records in their order, and Shift+Tab back.
	focus("go")
	var order []string
	for _, shift := range []int{0, 0, 0, modShift} {
		press(t, browser, "Tab", shift)
		order = append(order, browser.String(`document.activeElement.dataset.node ?? document.activeElement.tagName`))
	}
	if got := strings.Join(order, ","); got != "q,results,done,results" {
		t.Errorf("Tab from the first focus record walks %s, want q,results,done,results", got)
	}
}

// TestPageKeepsControlsAcrossAnUpdate: an update changes the controls in place. Every element that stood for a node
// is the same element after it, with the new state, so the state's transition runs and nothing is drawn anew; with
// reduced motion the same change is immediate.
func TestPageKeepsControlsAcrossAnUpdate(t *testing.T) {
	browser, send := openFixtures(t)
	send(`/fx-a mount ` + controls("Places", 1, true, false, 3))
	browser.Wait(at("log") + ` !== null`)
	ids := `["title","sub","q","results","modes","prefs","go","plain","off","done","live","fast","prog","stops","coords","notes","log"]`
	browser.Run(`(() => { window.was = ` + ids + `.map((id) => ` + at(`' + id + '`) + `); window.options = [...` + at("results") + `.children]; window.ran = [];
		document.addEventListener("transitionrun", (event) => window.ran.push(event.target.getAttribute("role") + " " + event.propertyName)); ` + at("results") + `.focus(); })()`)
	if got := browser.String(`getComputedStyle(window.options[0]).transitionDuration`); got != "0.15s, 0.15s" {
		t.Errorf("an option's transitions last %s, want 0.15s, 0.15s", got)
	}
	send(`/fx-a update ` + controls("Places near you", 2, false, true, 4))
	browser.Wait(at("title") + `.textContent === "Places near you"`)
	got := browser.String(`JSON.stringify({
		same: ` + ids + `.every((id, i) => ` + at(`' + id + '`) + ` === window.was[i]) && [...` + at("results") + `.children].every((el, i) => el === window.options[i]),
		focused: document.activeElement === ` + at("results") + `,
		options: window.options.map((o) => o.getAttribute("aria-selected")).join(),
		checks: [` + at("done") + `.checked, ` + at("live") + `.checked].join(),
		progress: ` + at("prog") + `.value,
		current: [...` + at("stops") + `.children].map((r) => r.getAttribute("aria-current") ?? "").join(),
	})`)
	if want := `{"same":true,"focused":true,"options":"false,false,true","checks":"false,true","progress":4,"current":"true,"}`; got != want {
		t.Errorf("after the update the controls are\n  %s\nwant\n  %s", got, want)
	}
	browser.Wait(`window.ran.includes("option background-color")`)
	browser.Emulate("prefers-reduced-motion", "reduce")
	if got := browser.String(`getComputedStyle(window.options[0]).transitionDuration + "/" + getComputedStyle(document.querySelector(".spinner")).animationName`); got != "0s, 0s/none" {
		t.Errorf("with reduced motion an option's transitions and the spinner's animation are %s, want 0s, 0s/none", got)
	}
}

// TestPageDefaultTokensHaveContrast computes the contrast (WCAG 2) of every pair of text and surface tokens the
// default stylesheet puts together, in the light and in the dark scheme: text, and the ring of the focus and of a
// hovered region, at least 4.5 to 1. It also checks that the page draws with those tokens.
func TestPageDefaultTokensHaveContrast(t *testing.T) {
	browser, send := openFixtures(t)
	send(`/fx-a mount ` + controls("Places", 1, true, false, 3))
	browser.Wait(at("log") + ` !== null`)
	pairs := `[["fg","bg",4.5],["fg","surface",4.5],["fg","surface-2",4.5],["fg","selected",4.5],["fg","success-bg",4.5],["fg","error-bg",4.5],
		["fg-muted","bg",4.5],["fg-muted","surface",4.5],["fg-muted","surface-2",4.5],["fg-muted","selected",4.5],
		["accent","surface",4.5],["accent","bg",4.5],["accent","selected",4.5],["on-accent","accent",4.5],
		["success","surface",4.5],["warning","surface",4.5],["error","surface",4.5],["error","bg",4.5],
		["focus","surface",4.5],["focus","surface-2",4.5],["focus","bg",4.5],
		["fg","surface-raised",4.5],["fg-muted","surface-raised",4.5],["accent","surface-raised",4.5],["focus","surface-raised",4.5],["error","surface-raised",4.5]]`
	check := `(() => { const probe = document.body.appendChild(document.createElement("span"));
		const rgb = (value) => { probe.style.color = value; return getComputedStyle(probe).color; };
		const lum = (c) => { const [r, g, b] = c.match(/[\d.]+/g).slice(0, 3).map((v) => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
		const token = (name) => rgb("var(--view-" + name + ")");
		const low = ` + pairs + `.map(([text, surface, least]) => { const [hi, lo] = [lum(token(text)), lum(token(surface))].sort((a, b) => b - a); return [text + " on " + surface, (hi + 0.05) / (lo + 0.05), least]; })
			.filter(([, ratio, least]) => !(ratio >= least)).map(([pair, ratio]) => pair + " " + ratio.toFixed(2));
		const style = (el) => getComputedStyle(el), q = (s) => document.querySelector(s);
		const drawn = [[style(document.documentElement).color, "fg"], [style(document.documentElement).backgroundColor, "bg"], [style(q(".mount")).backgroundColor, "surface"], [style(q(".mount > header")).color, "fg-muted"],
			[style(q("[role=option][aria-selected=true]")).backgroundColor, "selected"], [style(q('[data-node="go"]')).color, "fg"], [style(q('[data-node="go"]')).backgroundColor, "surface"],
			[style(q('[data-node="q"]')).backgroundColor, "surface"], [style(q("#prompt button")).backgroundColor, "accent"], [style(q("#prompt button")).color, "on-accent"]]
			.filter(([colour, name]) => colour !== token(name)).map(([colour, name]) => name + " is drawn " + colour);
		const bg = token("bg");
		probe.remove();
		return JSON.stringify({ low, drawn, bg }); })()`
	seen := map[string]bool{}
	for _, scheme := range []string{"light", "dark"} {
		browser.Emulate("prefers-color-scheme", scheme)
		// A control moves to the scheme's colours by its transition.
		browser.Wait(`JSON.parse(` + check + `).drawn.length === 0`)
		got := browser.String(check)
		var result struct {
			Low, Drawn []string
			Bg         string
		}
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Low) != 0 || len(result.Drawn) != 0 {
			t.Errorf("%s scheme: pairs below their contrast %q, elements not drawn with their token %q", scheme, result.Low, result.Drawn)
		}
		seen[result.Bg] = true
	}
	if len(seen) != 2 {
		t.Errorf("the light and the dark scheme have the same background: %v", seen)
	}
}

// TestPageDrawsATitleAsTheCardsHeading: a mount's title is the heading of its card, in the type of a heading; a mount
// without a title has a small muted caption, its extension and key; and a title that the view's own first heading
// repeats is not drawn a second time.
func TestPageDrawsATitleAsTheCardsHeading(t *testing.T) {
	browser, send := openFixtures(t)
	send(`/fx-a mount {"title":"Places","view":{"root":{"kind":"text","text":"A place"}}}`)
	send(`/fx-b mount {"view":{"root":{"kind":"text","text":"A note"}}}`)
	browser.Wait(`document.querySelector('[data-key="fx-a"]') !== null && document.querySelector('[data-key="fx-b"]') !== null`)
	head := func(key string) string {
		return browser.String(`(() => { const span = document.querySelector('[data-key="` + key + `"] > header span'), s = getComputedStyle(span);
			return [span.textContent, s.fontSize, s.fontWeight, s.color === getComputedStyle(document.documentElement).color ? "text colour" : "muted", getComputedStyle(span.parentElement).display].join(); })()`)
	}
	if got, want := head("fx-a"), "Places,20px,600,text colour,flex"; got != want {
		t.Errorf("the header of a mount with a title is %s, want %s", got, want)
	}
	if got, want := head("fx-b"), "fixture-b / fx-b,13px,400,muted,flex"; got != want {
		t.Errorf("the header of a mount without a title is %s, want %s", got, want)
	}
	send(`/fx-a update {"title":"Places","view":{"root":{"kind":"vstack","children":[{"kind":"text","id":"h","text":"Places"},{"kind":"text","text":"A place"}]}},"a11y":[{"node":"h","role":"heading"}]}`)
	browser.Wait(`document.querySelector('[data-key="fx-a"] h2') !== null`)
	if got, want := head("fx-a"), "Places,20px,600,text colour,none"; got != want {
		t.Errorf("the header of a mount whose view begins with its title is %s, want %s", got, want)
	}
}

// TestPageHasAColourForEveryThemeToken: a view and a drawing name their colours by Pi's theme tokens, and a token the
// page has no colour for draws nothing. Every colour of Pi's theme schema (schemas/theme.schema.json) is a custom
// property `--pig-<token>` of the default stylesheet that the browser computes to a colour, in the light and in the
// dark scheme.
func TestPageHasAColourForEveryThemeToken(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/theme.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Colors struct {
				Required   []string
				Properties map[string]json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	tokens := make([]string, 0, len(schema.Properties.Colors.Properties))
	for name := range schema.Properties.Colors.Properties {
		tokens = append(tokens, name)
	}
	sort.Strings(tokens)
	if len(tokens) < len(schema.Properties.Colors.Required) || len(schema.Properties.Colors.Required) < 51 {
		t.Fatalf("the theme schema names %d colours, %d of them required; want the 51 required ones at least", len(tokens), len(schema.Properties.Colors.Required))
	}
	list, err := json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	browser, _ := openFixtures(t)
	// A property that is not set computes to the fallback, and one that is no colour to the inherited colour.
	missing := `(() => { const around = document.body.appendChild(document.createElement("span")), probe = around.appendChild(document.createElement("span"));
		around.style.color = "rgb(3, 2, 1)";
		const bad = ` + string(list) + `.filter((token) => { probe.style.color = "var(--pig-" + token + ", rgb(1, 2, 3))"; const colour = getComputedStyle(probe).color; return !/^rgba?\(/.test(colour) || colour === "rgb(1, 2, 3)" || colour === "rgb(3, 2, 1)"; });
		around.remove();
		return bad.join(" "); })()`
	for _, scheme := range []string{"light", "dark"} {
		browser.Emulate("prefers-color-scheme", scheme)
		if got := browser.String(missing); got != "" {
			t.Errorf("%s scheme: the page has no colour for the theme tokens %s (of %d)", scheme, got, len(tokens))
		}
	}
}

// TestPageDrawsASceneAndItsRegions: a drawing is vector graphics. Each shape kind is its SVG element with the colour
// the view names (a hex colour as it is, a token as the page's colour for it); a region is a focusable element with
// its role and name (and the pointer's cursor for what it takes) that raises its intent for a click, a key and a drag, with the properties the intent's argument
// shape declares and no other; and a scene that is sent again changes in place: the same elements, moved. The scene
// has 5,000 points, and the test logs what an update of it costs.
func TestPageDrawsASceneAndItsRegions(t *testing.T) {
	b := startBridge(t, "-e", "testdata/scene.mjs")
	browser := browsertest.Start(t)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	browser.Run(recordFrames)
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.value = "/scene mount 5000"; form.requestSubmit(); })()`)
	browser.Wait(`document.querySelector('[data-key="scene"] svg') !== null`)
	shows := func(part string) string {
		return `document.querySelector('[data-key="scene"] .text').textContent.includes("` + part + `")`
	}
	got := browser.String(`(() => { const svg = document.querySelector('[data-key="scene"] svg'), k = (key) => svg.querySelector('[data-k="' + key + '"]');
		window.svg = svg; window.kept = ["land", "roads", "road-0", "route", "pin", "label", "region surface"].map(k);
		const fill = (key) => getComputedStyle(k(key)).fill, token = (name) => { const p = document.body.appendChild(document.createElement("span")); p.style.color = "var(--view-" + name + ")"; const c = getComputedStyle(p).color; p.remove(); return c; };
		return JSON.stringify({
			inside: svg.parentElement.dataset.node + "/" + svg.parentElement.children.length,
			box: svg.getAttribute("viewBox") + " " + svg.getAttribute("preserveAspectRatio") + " " + (Math.abs(svg.clientWidth / svg.clientHeight - 1.5) < 0.02),
			kinds: ["land", "roads", "road-0", "route", "scale", "park", "pin", "label", "zoom-face"].map((key) => k(key).tagName).join(),
			points: [...svg.querySelectorAll("polyline")].reduce((n, p) => n + p.points.length, 0),
			route: k("route").getAttribute("d") + " " + k("route").getAttribute("opacity"),
			roads: k("roads").getAttribute("transform"),
			colours: [fill("land") === token("surface-2"), fill("pin") === token("accent"), getComputedStyle(k("pin")).stroke, getComputedStyle(k("route")).fill, fill("park") === token("success-bg"), getComputedStyle(k("road-0")).stroke === token("fg-muted")].join(" "),
			label: k("label").textContent + " " + k("label").getAttribute("text-anchor") + " " + k("label").getAttribute("font-size"),
			regions: [...svg.querySelectorAll(".region")].map((r) => [r.tagName, r.getAttribute("role"), r.getAttribute("aria-label"), r.tabIndex, r.querySelector("title").textContent, getComputedStyle(r).cursor].join("/")).join(),
			text: k("credit").textContent,
			actions: document.querySelectorAll('[data-key="scene"] header button').length,
		}); })()`)
	want := `{"inside":"map/1","box":"0 0 960 640 xMidYMid meet true","kinds":"rect,g,polyline,path,line,polygon,ellipse,text,g","points":5000,` +
		`"route":"M100 500L300 420Q400 380 480 320C520 300 560 280 600 300Z 0.8","roads":"matrix(1 0 0 1 0 0)",` +
		`"colours":"true true rgb(255, 255, 255) none true true","label":"Harbour middle 14",` +
		`"regions":"rect/group/Map/0/Map/grab,rect/button/Zoom in/0/Zoom in/pointer,ellipse/button/Harbour/0/Harbour/pointer","text":"© OpenStreetMap contributors","actions":0}`
	if got != want {
		t.Errorf("the scene is drawn as\n  %s\nwant\n  %s", got, want)
	}
	if lines := accessible(t, browser, "button", "group"); !strings.Contains(strings.Join(lines, "\n"), `group "Map"`) || !strings.Contains(strings.Join(lines, "\n"), `button "Zoom in"`) || !strings.Contains(strings.Join(lines, "\n"), `button "Harbour"`) {
		t.Errorf("assistive technology is told of the regions %q, want the group Map and the buttons Zoom in and Harbour", lines)
	}
	// point is the whole pixel of the page nearest to a point of the drawing, and that pixel as a point of the drawing.
	browser.Run(`window.svg.scrollIntoView({ block: "center" })`)
	point := func(x, y float64) (px, py float64, drawing string) {
		t.Helper()
		var p struct {
			X, Y float64
			At   string
		}
		raw := browser.String(fmt.Sprintf(`(() => { const m = window.svg.getScreenCTM(), p = new DOMPoint(%v, %v).matrixTransform(m), x = Math.round(p.x), y = Math.round(p.y);
			const d = new DOMPoint(x, y).matrixTransform(m.inverse()), round = (v) => Math.round(v * 1000) / 1000; return JSON.stringify({ x, y, at: round(d.x) + "," + round(d.y) }); })()`, x, y))
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		return p.X, p.Y, p.At
	}
	// A click on the surface is its release and its activate gesture, at the point of the drawing that was clicked.
	x, y, clicked := point(200, 100)
	mouse(t, browser, "mousePressed", x, y)
	mouse(t, browser, "mouseReleased", x, y)
	browser.Wait(shows("at " + clicked + " released 1"))
	// The keyboard: Enter on the focused button region, and an arrow key on the surface, which is a drag of an eighth
	// of the viewport and its release.
	browser.Run(`window.svg.querySelector('[data-region="zoom"]').focus()`)
	press(t, browser, "Enter", 0)
	browser.Wait(shows("zoom 2 "))
	browser.Run(`window.svg.querySelector('[data-region="surface"]').focus()`)
	press(t, browser, "ArrowRight", 0)
	browser.Wait(shows("scene 120,0 zoom 2") + ` && ` + shows("released 2"))
	// A drag with the pointer: the scene follows it. The pin is where it was plus what the pointer moved.
	browser.Wait(`window.svg.getAnimations({ subtree: true }).length === 0`)
	before := browser.String(`JSON.stringify([window.kept[4].getBoundingClientRect().x, window.kept[1].getAttribute("transform")])`)
	x, y, _ = point(300, 500)
	mouse(t, browser, "mousePressed", x, y)
	for i := 1; i <= 8; i++ {
		mouse(t, browser, "mouseMoved", x+float64(6*i), y+float64(3*i))
	}
	mouse(t, browser, "mouseReleased", x+48, y+24)
	browser.Wait(shows("released 3"))
	got = browser.String(`(() => { const [x, transform] = ` + before + `; const k = (key) => window.svg.querySelector('[data-k="' + key + '"]');
		const scale = window.svg.getScreenCTM().a, moved = window.kept[4].getBoundingClientRect().x - x;
		const drags = window.frames.map((f) => JSON.parse(f)).filter((f) => f.arg.gesture === "drag").slice(1);
		return JSON.stringify({
			same: ["land", "roads", "road-0", "route", "pin", "label", "region surface"].every((key, i) => k(key) === window.kept[i]),
			followed: Math.abs(moved - 48) < 1,
			summed: Math.abs(drags.reduce((n, f) => n + f.arg.dx, 0) * scale - 48) < 1 && Math.abs(drags.reduce((n, f) => n + f.arg.dy, 0) * scale - 24) < 1,
			moved: transform !== window.kept[1].getAttribute("transform") && /^matrix\(2 0 0 2 [\d.]+ [\d.]+\)$/.test(window.kept[1].getAttribute("transform")),
			coalesced: drags.length >= 1 && drags.length <= 8,
		}); })()`)
	if want := `{"same":true,"followed":true,"summed":true,"moved":true,"coalesced":true}`; got != want {
		t.Errorf("after the drag the scene is\n  %s\nwant\n  %s", got, want)
	}
	// What each gesture sent: the properties its intent declares and no other. scene.zoom declares none.
	var frames []struct {
		Intent string         `json:"intent"`
		Arg    map[string]any `json:"arg"`
	}
	if err := json.Unmarshal([]byte(browser.String(`"[" + window.frames.join(",") + "]"`)), &frames); err != nil {
		t.Fatal(err)
	}
	var sent []string
	for _, f := range frames {
		var names []string
		for _, name := range []string{"gesture", "region", "x", "y", "dx", "dy"} {
			if _, ok := f.Arg[name]; ok {
				names = append(names, name)
			}
		}
		line := fmt.Sprintf("%s %v %d", f.Intent, names, len(f.Arg))
		if f.Arg["gesture"] != nil {
			line += " " + f.Arg["gesture"].(string)
		}
		if len(sent) == 0 || sent[len(sent)-1] != line {
			sent = append(sent, line)
		}
	}
	all := "[gesture region x y dx dy] 6"
	wantSent := []string{"scene.touch " + all + " release", "scene.touch " + all + " activate", "scene.zoom [] 0", "scene.touch " + all + " drag", "scene.touch " + all + " release", "scene.touch " + all + " drag", "scene.touch " + all + " release"}
	if strings.Join(sent, "\n") != strings.Join(wantSent, "\n") {
		t.Errorf("the gestures sent\n%s\nwant\n%s", strings.Join(sent, "\n"), strings.Join(wantSent, "\n"))
	}
	t.Logf("a scene of 5000 points: %s", browser.String(`"the last draw took " + window.uiView.timing.draw.toFixed(1) + " ms; from the intent's command to the scene drawn again " + window.uiView.timing.intent.toFixed(1) + " ms; from the pointer's move, with the wait for the drag's turn, " + window.uiView.timing.gesture.toFixed(1) + " ms; " + window.frames.length + " intents, snapshot " + JSON.stringify([...window.uiView.mounts.values()][0].snapshot).length + " bytes"`))
	if got := browser.String(`String(window.uiView.reports) + "/" + String(window.uiView.resyncs)`); got != "0/0" {
		t.Errorf("reports/resyncs = %s, want 0/0", got)
	}
}

// TestPageDrawsADialogMountAsADialog: a mount of the dialog slot is a modal dialog element named by its mount. It
// takes the focus when it opens, Escape does not close it (the mount is the extension's), hiding the mount closes it
// and gives the focus back, and showing it opens it again.
func TestPageDrawsADialogMountAsADialog(t *testing.T) {
	browser, send := openFixtures(t)
	browser.Run(`document.getElementById("prompt").elements.message.focus()`)
	send(`/fx-a mount {"slot":"dialog","view":{"root":{"kind":"vstack","children":[{"kind":"text","text":"Pick a place"},{"kind":"input","id":"q","value":""}]}},"focus":[{"node":"q","request":"mount"}]}`)
	browser.Wait(`document.querySelector('[data-key="fx-a"]') !== null`)
	state := `(() => { const el = document.querySelector('[data-key="fx-a"]'); return [el.tagName, el.open, el.matches(":modal"), el.getAttribute("aria-label"), el.contains(document.activeElement) ? document.activeElement.dataset.node : document.activeElement.name].join(); })()`
	if got, want := browser.String(state), "DIALOG,true,true,fixture-a / fx-a,q"; got != want {
		t.Errorf("the dialog mount is %s, want %s", got, want)
	}
	// A dialog stands on the raised surface, a step above a card in the dark scheme.
	face := `(() => { const probe = document.body.appendChild(document.createElement("span")); const faces = ["surface-raised", "surface"].map((name) => { probe.style.color = "var(--view-" + name + ")"; return getComputedStyle(probe).color; });
		probe.remove(); return [getComputedStyle(document.querySelector('[data-key="fx-a"]')).backgroundColor === faces[0], faces[0] !== faces[1]].join(); })()`
	browser.Emulate("prefers-color-scheme", "dark")
	if got := browser.String(face); got != "true,true" {
		t.Errorf("in the dark scheme: the dialog's face is the raised surface, the raised surface differs from a card's = %s, want true,true", got)
	}
	press(t, browser, "Escape", 0)
	press(t, browser, "Escape", 0)
	if got, want := browser.String(state), "DIALOG,true,true,fixture-a / fx-a,q"; got != want {
		t.Errorf("after Escape the dialog mount is %s, want %s", got, want)
	}
	send(`/fx-a hide`)
	browser.Wait(`document.querySelector('[data-key="fx-a"]').hidden`)
	if got, want := browser.String(state), "DIALOG,false,false,fixture-a / fx-a,message"; got != want {
		t.Errorf("hidden, the dialog mount is %s, want %s", got, want)
	}
	send(`/fx-a show`)
	browser.Wait(`!document.querySelector('[data-key="fx-a"]').hidden`)
	if got, want := browser.String(state), "DIALOG,true,true,fixture-a / fx-a,"; !strings.HasPrefix(got, want) {
		t.Errorf("shown again, the dialog mount is %s, want %s and the focus inside it", got, want)
	}
}

// TestPageDialogTakesNoFocusFromTypedText: a dialog mount that arrives while the user is typing in a text field takes
// no focus from it. It opens without being modal, the page reports `unfocused`, and it becomes modal when the user
// enters it.
func TestPageDialogTakesNoFocusFromTypedText(t *testing.T) {
	browser, _ := openFixtures(t)
	// A page that is not the focused window is sent no focus event; the test's page is told it is.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := browser.Call(ctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.value = '/fx-b mount {"slot":"dialog","view":{"root":{"kind":"input","id":"name","value":""}},"focus":[{"node":"name","request":"mount"}]}';
		form.requestSubmit(); form.elements.message.value = "half a thought"; form.elements.message.focus(); })()`)
	browser.Wait(`document.querySelector('[data-key="fx-b"]') !== null`)
	state := `(() => { const el = document.querySelector('[data-key="fx-b"]'); return [el.open, el.matches(":modal"), document.activeElement.name ?? document.activeElement.dataset.node, document.getElementById("prompt").elements.message.value, window.uiView.reports].join(); })()`
	if got, want := browser.String(state), "true,false,message,half a thought,1"; got != want {
		t.Errorf("the dialog that arrived while the user typed is %s, want %s", got, want)
	}
	browser.Run(`document.querySelector('[data-key="fx-b"] input').focus()`)
	if got, want := browser.String(state), "true,true,,half a thought,1"; got != want {
		t.Errorf("entered, the dialog is %s, want %s", got, want)
	}
	if got := browser.String(`document.activeElement.dataset.node`); got != "name" {
		t.Errorf("the focus is on %q, want the dialog's input", got)
	}
}

// TestPageLabelsAMountAndFiresItsBindings: a mount is labelled with its title. A key binding with a name is a button
// that shows its key; a key binding fires while the focus is inside its mount and the focused control did not take
// the key, with the binding's argument or {}; outside the mount it fires nothing. A bound intent is not also a header
// button, and the page reports the gesture binding it cannot honour.
func TestPageLabelsAMountAndFiresItsBindings(t *testing.T) {
	browser, send := openFixtures(t)
	intent := func(name, arg string) string {
		return `{"name":"fx.` + name + `","arg":` + arg + `,"timeoutMs":100}`
	}
	send(`/fx-a mount {"title":"Day plan","view":{"root":{"kind":"vstack","children":[{"kind":"text","text":"Today"},{"kind":"input","id":"q","value":""}]}},` +
		`"intents":[` + intent("close", `{"type":"object"}`) + `,` + intent("save", `{"type":"object","properties":{"how":{"type":"string"}}}`) + `,` + intent("help", `{"type":"object"}`) + `,` + intent("more", `{"type":"object"}`) + `],` +
		`"bindings":[{"key":"q","intent":"fx.close","name":"Close"},{"key":"ctrl+s","intent":"fx.save","arg":{"how":"draft"},"name":"Save draft"},{"key":"?","intent":"fx.help"},{"gesture":"swipe-left","intent":"fx.close"}]}`)
	browser.Wait(at("q") + ` !== null`)
	browser.Run(recordFrames)
	got := browser.String(`(() => { const m = document.querySelector('[data-key="fx-a"]'); return JSON.stringify({
		tab: document.querySelector("[role=tab]").textContent, caption: m.querySelector("header span").textContent,
		hints: [...m.querySelectorAll(".hints button")].map((b) => [b.firstChild.textContent, b.querySelector("kbd").textContent, b.getAttribute("aria-keyshortcuts")].join("|")).join(),
		toolbar: m.querySelector(".hints").getAttribute("role"), header: [...m.querySelectorAll("header button")].map((b) => b.textContent).join(),
		whole: m.querySelector(".view").tabIndex, reports: window.uiView.reports }); })()`)
	if want := `{"tab":"Day plan","caption":"Day plan","hints":"Close|Q|Q,Save draft|Ctrl+S|Control+S","toolbar":"toolbar","header":"More","whole":0,"reports":1}`; got != want {
		t.Errorf("the mount with a title and bindings is\n  %s\nwant\n  %s", got, want)
	}
	sent := 0
	raised := func() {
		t.Helper()
		sent++
		browser.Wait(`window.frames.length === ` + strconv.Itoa(sent))
	}
	browser.Run(`document.querySelector('[data-key="fx-a"] .view').focus()`)
	press(t, browser, "q", 0)
	raised() // fx.close {}
	press(t, browser, "?", modShift)
	raised() // fx.help {}
	browser.Run(at("q") + `.focus()`)
	press(t, browser, "q", 0) // typed into the input
	press(t, browser, "s", modCtrl)
	raised() // fx.save {"how":"draft"}
	browser.Run(`document.querySelector('[data-key="fx-a"] .hints button').click()`)
	raised() // fx.close {}
	browser.Run(`document.getElementById("prompt").elements.message.focus()`)
	press(t, browser, "q", 0) // outside the mount
	got = browser.String(`JSON.stringify([window.frames.map((f) => { f = JSON.parse(f); return [f.intent, f.nodeKey ?? "", JSON.stringify(f.arg)].join(" "); }), ` + at("q") + `.value, document.getElementById("prompt").elements.message.value])`)
	if want := `[["fx.close  {}","fx.help  {}","fx.save  {\"how\":\"draft\"}","fx.close  {}"],"q","q"]`; got != want {
		t.Errorf("the bindings raised, and the fields hold,\n  %s\nwant\n  %s", got, want)
	}
}

// TestPageDrawsAGrid: a grid node is a grid of equal columns with its head, its cells, at most chipRows chips in a
// cell and a line that counts the rest. A cell with a value is chosen with the arrow keys and Enter or with a click,
// which raises the grid's intent with the cell's value; an update that keeps `selected` keeps the user's cursor and
// every element.
func TestPageDrawsAGrid(t *testing.T) {
	browser, send := openFixtures(t)
	content := func(first, selected string) string {
		return `{"view":{"root":{"kind":"grid","id":"cal","columns":3,"head":["Mon","Tue","Wed"],"chipRows":2,"selected":"` + selected + `","cells":[` +
			`{"value":"d1","label":"` + first + `","chips":[{"text":"Standup","color":"#2e9e4f"}]},` +
			`{"value":"d2","label":"2","mark":"current","chips":[{"text":"Review"},{"text":"Lunch"},{"text":"Call"}],"more":1},` +
			`{"label":"3","dim":true},{"value":"d4","label":"4","color":"accent"},{"value":"d5","label":"5"}]}},` +
			`"intents":[{"name":"fx.day","nodeKey":"cal","arg":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},"timeoutMs":100}],"a11y":[{"node":"cal","name":"June"}]}`
	}
	send(`/fx-a mount ` + content("1", "d2"))
	browser.Wait(at("cal") + ` !== null`)
	browser.Run(recordFrames)
	state := `(() => { const g = ` + at("cal") + `, cells = [...g.querySelectorAll("[role=gridcell]")], rect = (el) => el.getBoundingClientRect();
		return JSON.stringify({ role: g.getAttribute("role") + "/" + g.getAttribute("aria-label"), rows: g.querySelectorAll("[role=row]").length, head: [...g.querySelectorAll("[role=columnheader]")].map((h) => h.textContent).join(),
			columns: getComputedStyle(g).gridTemplateColumns.split(" ").length + "/" + (Math.abs(rect(cells[0]).width - rect(cells[2]).width) < 1 && rect(cells[3]).top > rect(cells[0]).top),
			cells: cells.map((c) => [...c.children].map((s) => s.textContent).join("|") + (c.getAttribute("aria-selected") === "true" ? "*" : "") + (c.getAttribute("aria-current") ? "@" : "") + (c.dataset.value ? "" : "-")).join(","),
			chip: getComputedStyle(cells[0].querySelector(".chip")).borderLeftColor, cursor: document.getElementById(g.getAttribute("aria-activedescendant")).dataset.value }); })()`
	if got, want := browser.String(state), `{"role":"grid/June","rows":3,"head":"Mon,Tue,Wed","columns":"3/true","cells":"1|Standup,2|Review|Lunch|+2 more*@,3-,4,5","chip":"rgb(46, 158, 79)","cursor":"d2"}`; got != want {
		t.Errorf("the grid is\n  %s\nwant\n  %s", got, want)
	}
	if lines := strings.Join(accessible(t, browser, "grid", "columnheader", "gridcell"), "\n"); !strings.Contains(lines, `grid "June"`) || strings.Count(lines, "columnheader ") != 3 || strings.Count(lines, "gridcell ") != 5 {
		t.Errorf("assistive technology is told\n%s\nwant the grid June with 3 column headers and 5 cells", lines)
	}
	browser.Run(`(window.gridWas = ` + at("cal") + `).focus()`)
	// Right passes over the cell without a value; left comes back; down is the cell under.
	var walked []string
	for _, key := range []string{"ArrowRight", "ArrowLeft", "ArrowDown"} {
		press(t, browser, key, 0)
		walked = append(walked, browser.String(at("cal")+`.querySelector('[aria-selected="true"]').dataset.value`))
	}
	if got := strings.Join(walked, ","); got != "d4,d2,d5" {
		t.Errorf("the arrows walk %s, want d4,d2,d5", got)
	}
	press(t, browser, "Enter", 0)
	browser.Wait(`window.frames.length === 1`)
	browser.Run(at("cal") + `.querySelectorAll("[role=gridcell]")[2].click()`) // no value: nothing
	browser.Run(at("cal") + `.querySelector("[role=gridcell]").click()`)
	browser.Wait(`window.frames.length === 2`)
	if got, want := browser.String(`window.frames.map((f) => { f = JSON.parse(f); return f.intent + " " + f.nodeKey + " " + f.arg.value; }).join()`), "fx.day cal d5,fx.day cal d1"; got != want {
		t.Errorf("choosing cells raised %s, want %s", got, want)
	}
	// An update that keeps `selected` keeps the user's cursor (d1) and the grid's element; one that moves it moves it.
	send(`/fx-a update ` + content("1 June", "d2"))
	browser.Wait(at("cal") + `.textContent.includes("1 June")`)
	if got := browser.String(`[` + at("cal") + ` === window.gridWas, ` + at("cal") + `.querySelector('[aria-selected="true"]').dataset.value].join()`); got != "true,d1" {
		t.Errorf("after an update that keeps selected: same element, cursor = %s, want true,d1", got)
	}
	send(`/fx-a update ` + content("1 Jun", "d4"))
	browser.Wait(at("cal") + `.textContent.includes("1 Jun") && !` + at("cal") + `.textContent.includes("1 June")`)
	if got := browser.String(at("cal") + `.querySelector('[aria-selected="true"]').dataset.value`); got != "d4" {
		t.Errorf("after an update that moves selected the cursor is %s, want d4", got)
	}
}

// TestPageShowsNoCountersAndOneColumnUntilThereIsATranscript: the default page shows no line of the carrier's numbers
// (the address asks for it with ?debug), and while its chat holds no line the panels and the prompt are one centred
// column of at most 880 px; under a panel taller than the window the page scrolls, the prompt stays in sight and what
// is scrolled into view stops above it; with
// a line in the chat the panels stand beside it.
func TestPageShowsNoCountersAndOneColumnUntilThereIsATranscript(t *testing.T) {
	b := startBridge(t, "-e", "testdata/scene.mjs")
	browser := browsertest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	window := func(height int) {
		t.Helper()
		if _, err := browser.Call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": 1280, "height": height, "deviceScaleFactor": 1, "mobile": false}); err != nil {
			t.Fatal(err)
		}
	}
	window(800)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	// The command is sent without a line in the chat, as an extension's own trigger would mount the panel.
	browser.Run(`fetch("/send?session=" + window.uiView.session, { method: "POST", headers: { Authorization: ` + strconv.Quote(b.auth(t)) + ` }, body: JSON.stringify({ id: "t1", type: "prompt", message: "/scene mount 10" }) })`)
	browser.Wait(`document.querySelector('[data-key="scene"] svg') !== null`)
	layout := `(() => { const state = document.getElementById("state"), panels = document.getElementById("panels").getBoundingClientRect(), prompt = document.getElementById("prompt").getBoundingClientRect();
		return JSON.stringify({ state: state.textContent + "/" + getComputedStyle(state).display, width: panels.width, centred: Math.abs(panels.left + panels.right - document.documentElement.clientWidth) < 2, promptUnder: prompt.top >= panels.bottom, beside: prompt.right <= panels.left }); })()`
	if got, want := browser.String(layout), `{"state":"/none","width":880,"centred":true,"promptUnder":true,"beside":false}`; got != want {
		t.Errorf("without a transcript the page is\n  %s\nwant\n  %s", got, want)
	}
	// In a window lower than the panel the page scrolls and the prompt stays in sight, under the panel.
	window(480)
	fits := `(() => { const prompt = document.getElementById("prompt").getBoundingClientRect(), page = document.documentElement, at = document.elementFromPoint(640, prompt.top + prompt.height / 2);
		const fits = { pageScrolls: page.scrollHeight > page.clientHeight, promptInSight: prompt.top > 0 && prompt.bottom <= innerHeight, promptOnTop: at?.closest("#prompt") !== null };
		// What the browser scrolls into view, as it does for the focus, is not under the prompt: a press on it reaches the panel.
		const low = document.querySelector('[data-key="scene"] [data-k="credit"]');
		low.scrollIntoView({ block: "nearest" });
		const box = low.getBoundingClientRect();
		fits.scrolledIntoViewIsHit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2)?.closest('[data-key="scene"]') != null;
		return JSON.stringify(fits); })()`
	if got, want := browser.String(fits), `{"pageScrolls":true,"promptInSight":true,"promptOnTop":true,"scrolledIntoViewIsHit":true}`; got != want {
		t.Errorf("in a window lower than the panel the page is\n  %s\nwant\n  %s", got, want)
	}
	window(800)
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.value = "hello"; form.requestSubmit(); })()`)
	browser.Wait(`document.querySelector(".line.user") !== null`)
	if got, want := browser.String(`JSON.stringify(JSON.parse(`+layout+`).beside)`), "true"; got != want {
		t.Errorf("with a line in the chat the prompt stands beside the panels: %s, want %s", got, want)
	}
	// The same tab holds the session secret already: no second address is needed.
	browser.Open(b.base + "/?debug")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	if got := browser.String(`document.getElementById("state").textContent`); got != "attachment 1, event 0, 0 mounts" {
		t.Errorf("with ?debug the status line reads %q, want the carrier's numbers", got)
	}
}

// TestPageOffersAHiddenMountThatDeclaresWhenHidden: a hidden mount that declares a `whenHidden` intent is listed by its
// title among the page's hidden views, and choosing it raises that intent with {} and no node key; the extension's
// handler shows the mount, and the list is empty again. A hidden mount without the intent is not listed, and a live
// mount has no header button for the intent.
func TestPageOffersAHiddenMountThatDeclaresWhenHidden(t *testing.T) {
	browser, send := openWith(t, "testdata/fixture-a.mjs", "testdata/fixture-c.mjs")
	send(`/fx-c mount {"title":"Places","view":{"root":{"kind":"text","text":"A place"}},"intents":[{"name":"fx.open","arg":{"type":"object"},"whenHidden":true}]}`)
	send(`/fx-a mount {"title":"Notes","view":{"root":{"kind":"text","text":"A note"}}}`)
	browser.Wait(`document.querySelector('[data-key="fx-a"]') !== null && document.querySelector('[data-key="fx-c"]') !== null`)
	offered := `(() => { const list = document.getElementById("closed"); return JSON.stringify({ hidden: list.hidden, offered: [...list.querySelectorAll("button")].map((b) => b.textContent), header: document.querySelectorAll('[data-key="fx-c"] header button').length }); })()`
	if got, want := browser.String(offered), `{"hidden":true,"offered":[],"header":0}`; got != want {
		t.Errorf("with both mounts live the page offers %s, want %s", got, want)
	}
	browser.Run(recordFrames)
	send(`/fx-c hide`)
	send(`/fx-a hide`)
	browser.Wait(`document.querySelector('[data-key="fx-a"]').hidden && document.querySelector('[data-key="fx-c"]').hidden`)
	if got, want := browser.String(offered), `{"hidden":false,"offered":["Places"],"header":0}`; got != want {
		t.Errorf("with both mounts hidden the page offers %s, want %s", got, want)
	}
	browser.Run(`document.querySelector("#closed button").click()`)
	browser.Wait(`window.frames.length === 1 && !document.querySelector('[data-key="fx-c"]').hidden`)
	if got, want := browser.String(`(() => { const f = JSON.parse(window.frames[0]); return [f.intent, JSON.stringify(f.arg), "nodeKey" in f, f.mount.key].join(" "); })()`), "fx.open {} false fx-c"; got != want {
		t.Errorf("choosing the hidden mount posted %q, want %q", got, want)
	}
	if got, want := browser.String(offered), `{"hidden":true,"offered":[],"header":0}`; got != want {
		t.Errorf("with the mount shown again the page offers %s, want %s", got, want)
	}
	if got := browser.String(`String(document.querySelector('[data-key="fx-a"]').hidden)`); got != "true" {
		t.Errorf("the mount without the intent is hidden: %s, want true", got)
	}
}

// TestPageRaisesTheDismissIntentOfADialogOrOverlay: a mount of the dialog or the overlay slot that declares a `dismiss`
// intent takes the user's leaving: Escape inside it, or the dialog element's `cancel` event (the browser's request to
// close it), raises that intent once with {} and no node key, and the page closes nothing itself: the extension's
// handler hides the mount. A mount that declares none posts nothing and stays open, and in the panel slot the member
// has no effect.
func TestPageRaisesTheDismissIntentOfADialogOrOverlay(t *testing.T) {
	browser, send := openWith(t, "testdata/fixture-a.mjs", "testdata/fixture-b.mjs", "testdata/fixture-c.mjs", "testdata/fixture-d.mjs")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := browser.Call(ctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	browser.Run(recordFrames)
	content := func(slot, intents string) string {
		return `{"slot":"` + slot + `","title":"Save place","view":{"root":{"kind":"input","id":"name","value":""}},"focus":[{"node":"name","request":"mount"}],"intents":[` + intents + `]}`
	}
	const closes = `{"name":"fx.close","arg":{"type":"object"},"dismiss":true}`
	el := func(key string) string { return `document.querySelector('[data-key="` + key + `"]')` }
	inside := func(key string) string {
		return el(key) + ` !== null && ` + el(key) + `.open === true && ` + el(key) + `.contains(document.activeElement)`
	}
	var want []string
	for _, c := range []struct{ slot, key string }{{"dialog", "fx-c"}, {"overlay", "fx-d"}} {
		send(`/` + c.key + ` mount ` + content(c.slot, closes))
		browser.Wait(inside(c.key) + ` && ` + el(c.key) + `.closest("section").dataset.slot === "` + c.slot + `"`)
		press(t, browser, "Escape", 0)
		want = append(want, c.key+" fx.close {} false")
		browser.Wait(`window.frames.length === ` + strconv.Itoa(len(want)) + ` && ` + el(c.key) + `.hidden && !` + el(c.key) + `.open`)
		send(`/` + c.key + ` show`)
		browser.Wait(`!` + el(c.key) + `.hidden && ` + el(c.key) + `.open`)
		if got := browser.String(`String(` + el(c.key) + `.dispatchEvent(new Event("cancel", { cancelable: true })))`); got != "false" {
			t.Errorf("%s: the dialog's cancel event was not prevented, so the browser would close the mount", c.slot)
		}
		want = append(want, c.key+" fx.close {} false")
		browser.Wait(`window.frames.length === ` + strconv.Itoa(len(want)) + ` && ` + el(c.key) + `.hidden`)
	}
	if got := browser.String(`window.frames.map((f) => { f = JSON.parse(f); return [f.mount.key, f.intent, JSON.stringify(f.arg), "nodeKey" in f].join(" "); }).join("; ")`); got != strings.Join(want, "; ") {
		t.Errorf("leaving a mount that declares a dismiss intent posted\n  %s\nwant\n  %s", got, strings.Join(want, "; "))
	}

	// In the panel slot the member has no effect.
	send(`/fx-b mount ` + content("panel", closes))
	browser.Wait(el("fx-b") + `?.tagName === "ARTICLE" && ` + el("fx-b") + `.querySelector("input") !== null`)
	browser.Run(el("fx-b") + `.querySelector("input").focus()`)
	press(t, browser, "Escape", 0)
	// Without the member nothing is posted and the mount stays as it is.
	send(`/fx-a mount ` + content("dialog", `{"name":"fx.other","arg":{"type":"object"}}`))
	browser.Wait(inside("fx-a"))
	press(t, browser, "Escape", 0)
	browser.Run(el("fx-a") + `.dispatchEvent(new Event("cancel", { cancelable: true }))`)
	press(t, browser, "Escape", 0)
	// A frame the page posts is recorded before the key's event returns.
	state := `[window.frames.length, ` + el("fx-a") + `.open, ` + el("fx-a") + `.matches(":modal"), ` + el("fx-b") + `.hidden].join()`
	if got, want := browser.String(state), strconv.Itoa(len(want))+",true,true,false"; got != want {
		t.Errorf("a panel that declares the member and a dialog without it: frames, dialog open, modal, panel hidden = %s, want %s", got, want)
	}
}
