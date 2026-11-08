// Ports the browser-side terminal tests of Pi's test/p2: terminal-clipboard.test.ts (2 cases), terminal-reconnect.test.ts and
// terminal-input.test.ts, against the vendored web client in headless Chromium (p2test.Chromium: CHROMIUM_BIN, else
// chromium-docker on the PATH; the cases are skipped without one). terminal-input is a pure function of the vendored
// terminal-input.ts: Node (which strips the types) runs it.
package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// terminalPage is a page showing a new terminal; its socket is wrapped so tests see the sent bytes and inject output.
type terminalPage struct {
	*p2test.Chromium
	t      *testing.T
	origin string
}

func (p *terminalPage) call(method string, params any) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.t.Context(), 30*time.Second)
	defer cancel()
	if _, err := p.Call(ctx, method, params); err != nil {
		p.t.Fatalf("%s: %v", method, err)
	}
}

// eval is the value of expression in the page, decoded into out.
func (p *terminalPage) eval(expression string, out any) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.t.Context(), 30*time.Second)
	defer cancel()
	if err := p.Evaluate(ctx, expression, out); err != nil {
		p.t.Fatal(err)
	}
}

func (p *terminalPage) bool(expression string) bool {
	p.t.Helper()
	var value bool
	p.eval(expression, &value)
	return value
}

func (p *terminalPage) number(expression string) int {
	p.t.Helper()
	var value int
	p.eval(expression, &value)
	return value
}

func (p *terminalPage) text(expression string) string {
	p.t.Helper()
	var value string
	p.eval(expression, &value)
	return value
}

type point struct{ X, Y float64 }

func (p *terminalPage) point(expression string) point {
	p.t.Helper()
	var value point
	p.eval(`(()=>{const p=`+expression+`;return {X:p.x,Y:p.y}})()`, &value)
	return value
}

// output is output from the program, straight into the page's terminal (the shell does not see it).
func (p *terminalPage) output(text string) {
	p.t.Helper()
	encoded, _ := json.Marshal(text)
	p.Run(`window.terminalSocket.dispatchEvent(new MessageEvent('message',{data:new TextEncoder().encode(` + string(encoded) + `).buffer}))`)
}

func (p *terminalPage) lastBytes() string {
	p.t.Helper()
	return p.text("new TextDecoder().decode(new Uint8Array(window.terminalBytes.at(-1)))")
}

func (p *terminalPage) touch(kind string, points ...point) {
	p.t.Helper()
	touches := []map[string]any{}
	for _, each := range points {
		touches = append(touches, map[string]any{"x": each.X, "y": each.Y, "id": 1})
	}
	p.call("Input.dispatchTouchEvent", map[string]any{"type": kind, "touchPoints": touches})
}

const terminalSocketWrapper = `
window.terminalBytes=[];
window.terminalSockets=[];
window.WebSocket=new Proxy(window.WebSocket,{construct(Target,args){
	const socket=Reflect.construct(Target,args);
	if(String(args[0]).includes('/terminal')){
		window.terminalSocket=socket;
		window.terminalSockets.push(socket);
		const send=socket.send.bind(socket);
		socket.send=data=>{if(typeof data==='string')send(data);else window.terminalBytes.push([...new Uint8Array(data.buffer??data,data.byteOffset??0,data.byteLength)]);};
	}
	return socket;
}});`

// withTerminal opens the terminal panel of a new session in Chromium (with a touch screen when touch), after run is given the page.
func withTerminal(t *testing.T, touch bool, run func(page *terminalPage)) {
	t.Helper()
	if p2test.ChromiumBinary(t.Context()) == "" {
		t.Skip("no Chromium: set CHROMIUM_BIN (or put chromium-docker on the PATH)")
	}
	bundle, err := web.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	// The terminal button opens the panel in the workspace (Pi's `terminalPlacement`) or at the bottom of the chat; both are
	// slots another plugin hosts, and the workspace plugin's web facet is not in the bundle yet.
	if !slices.Contains(bundle.Plugins, "workspace") {
		t.Skip("the web bundle has no workspace plugin (its web facet is the workspace lane's): the terminal panel has no host to open in")
	}
	p2test.UseTerminalDaemon(t)
	enabled := true
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Web: &enabled})
	t.Cleanup(func() { shutdownTerminalHost(t, fixture.DataDir) })
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	id := object(created).Value("id").(string)
	page := &terminalPage{Chromium: p2test.StartChromium(t), t: t, origin: "http://127.0.0.1:" + strconv.Itoa(fixture.Server.Port)}
	t.Cleanup(func() {
		if t.Failed() {
			var state string
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
			defer cancel()
			_ = page.Evaluate(ctx, `JSON.stringify({text: document.body.innerText.slice(0, 1500), html: [...document.querySelectorAll('[class*=terminal], [aria-label*=erminal]')].map(e => e.outerHTML.slice(0, 400)), sockets: (window.terminalSockets||[]).map(s => [s.url.replace(/token=[^&]*/, 'token=…'), s.readyState])})`, &state)
			t.Logf("the page: %s", state)
		}
	})
	if touch {
		page.call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 390, "height": 844, "mobile": true, "deviceScaleFactor": 1})
		page.call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 1})
	}
	page.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": terminalSocketWrapper})
	page.Open(page.origin + "/s/" + id)
	page.Wait("document.querySelector('button[aria-label=Terminal]')")
	page.Run("document.querySelector('button[aria-label=Terminal]').click()")
	if touch {
		page.Wait("document.querySelector('.terminal-row')?.textContent.includes('$') || document.querySelector('.terminal-row')?.textContent.includes('#')")
	} else {
		page.Wait("[...document.querySelectorAll('.terminal-row')].some(row => /[$#]/.test(row.textContent))")
	}
	run(page)
}

func pasteEvent(text string) string {
	encoded, _ := json.Marshal(text)
	return `(()=>{const data=new DataTransfer();data.setData('text/plain',` + string(encoded) + `);document.querySelector('.terminal-input').dispatchEvent(new ClipboardEvent('paste',{clipboardData:data,bubbles:true,cancelable:true}));})()`
}

func TestTouchTerminalALongPressOnABlankSpotIsANativePasteTargetClipboardKeyDenialScrolling(t *testing.T) {
	withTerminal(t, true, func(page *terminalPage) {
		page.Wait(`document.querySelector('button[aria-label="Paste into terminal"]')`)
		// A blank cell: the prompt is on the first row.
		blank := `(()=>{const r=document.querySelector('.terminal-content').getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+Math.min(70,r.height/2)}})()`
		at := page.point(blank)
		page.touch("touchStart", at)
		time.Sleep(700 * time.Millisecond)
		var native struct {
			UnderFinger    bool    `json:"underFinger"`
			Focused        bool    `json:"focused"`
			W              float64 `json:"width"`
			H              float64 `json:"height"`
			Pointer        string  `json:"pointer"`
			Selection      string  `json:"selection"`
			Background     string  `json:"background"`
			ContextAllowed bool    `json:"contextAllowed"`
		}
		page.eval(`(()=>{const e=document.querySelector('.terminal-input'),r=e.getBoundingClientRect(),s=getComputedStyle(e);
			const event=new MouseEvent('contextmenu',{bubbles:true,cancelable:true});
			return {underFinger:document.elementFromPoint(`+px(at.X)+`,`+px(at.Y)+`)===e,focused:document.activeElement===e,width:r.width,height:r.height,pointer:s.pointerEvents,selection:s.userSelect,background:s.backgroundColor,contextAllowed:e.dispatchEvent(event)}})()`, &native)
		if !native.UnderFinger || !native.Focused {
			t.Fatalf("native = %+v", native)
		}
		if native.W < 40 || native.H < 40 || native.Pointer != "auto" || native.Selection != "text" || native.Background != "rgba(0, 0, 0, 0)" || !native.ContextAllowed {
			t.Fatalf("native = %+v", native)
		}
		page.touch("touchEnd")
		page.Run(pasteEvent("plain\nlines"))
		if got := page.lastBytes(); got != "plain\rlines" {
			t.Fatalf("paste = %q", got)
		}
		// Mode 2004 is terminal output, so both UI entry points must use Ghostty's existing encoder.
		page.output("\x1b[?2004h")
		time.Sleep(100 * time.Millisecond)
		page.Run(pasteEvent("first\nsecond\u001bthird"))
		if got := page.lastBytes(); got != "\x1b[200~first\nsecond third\x1b[201~" {
			t.Fatalf("bracketed paste = %q", got)
		}
		page.call("Browser.grantPermissions", map[string]any{"origin": page.origin, "permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"}})
		page.Run("navigator.clipboard.writeText('toolbar\\npaste')")
		pasteAt := page.point(`(()=>{const r=document.querySelector('button[aria-label="Paste into terminal"]').getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}})()`)
		tapPaste := func() {
			page.touch("touchStart", pasteAt)
			page.touch("touchEnd")
		}
		tapPaste()
		page.Wait("window.terminalBytes.length === 3")
		if got := page.lastBytes(); got != "\x1b[200~toolbar\npaste\x1b[201~" {
			t.Fatalf("toolbar paste = %q", got)
		}
		page.call("Browser.setPermission", map[string]any{"origin": page.origin, "permission": map[string]any{"name": "clipboard-read"}, "setting": "denied"})
		tapPaste()
		page.Wait("document.querySelector('.terminal output')?.textContent.includes('Long-press')")
		if got := page.number("window.terminalBytes.length"); got != 3 {
			t.Fatalf("denial sent %d writes, want nothing", got-3)
		}
		// A swipe cancels the editing target and still moves through the terminal's scrollback.
		var lines strings.Builder
		for index := 0; index < 80; index++ {
			lines.WriteString("copy-line-" + strconv.Itoa(index) + "\r\n")
		}
		page.output(lines.String())
		page.Wait("document.querySelector('.terminal-content').textContent.includes('copy-line-79')")
		beforeScroll := page.text("document.querySelector('.terminal-row').textContent")
		// Clipboard denial adds a notice above the screen; use its current layout and a real multi-frame swipe.
		at = page.point(blank)
		page.touch("touchStart", at)
		for step := 1; step <= 3; step++ {
			page.touch("touchMove", point{at.X, at.Y + float64(step*20)})
			time.Sleep(30 * time.Millisecond)
		}
		time.Sleep(120 * time.Millisecond) // Lift after stopping, without a fling that redraws the selection below.
		page.touch("touchEnd")
		encoded, _ := json.Marshal(beforeScroll)
		page.Wait("document.querySelector('.terminal-row').textContent !== " + string(encoded))
		if page.bool("document.querySelector('.terminal-input').hasAttribute('data-native-menu')") {
			t.Fatal("a swipe left the native menu target")
		}
		page.Run(`(()=>{document.querySelector('.terminal-input').blur();const range=document.createRange();range.selectNodeContents([...document.querySelectorAll('.terminal-row')].find(row=>row.textContent.trim()));const selection=getSelection();selection.removeAllRanges();selection.addRange(range);})()`)
		selection := page.text("getSelection().getRangeAt(0).toString()")
		if strings.TrimSpace(selection) == "" || page.bool("getSelection().isCollapsed") {
			t.Fatalf("selection = %q", selection)
		}
		page.output("\x1b[?1000h")
		time.Sleep(100 * time.Millisecond)
		if !page.bool("document.querySelector('.terminal-row').dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true}))") {
			t.Fatal("Copy menu is gone when the program tracks the mouse")
		}
		page.touch("touchStart", at)
		time.Sleep(400 * time.Millisecond)
		if got := page.text("getSelection().getRangeAt(0).toString()"); got != selection {
			t.Fatalf("selection = %q, want %q", got, selection)
		}
		if page.bool("document.querySelector('.terminal-input').hasAttribute('data-native-menu')") {
			t.Fatal("a long press on a selection made the input the native menu target")
		}
		page.touch("touchEnd")
	})
}

func px(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func TestTouchTerminalALongPressOnOutputStaysNativeTheSelectionSurvivesOutputCopyJoinsWrappedRows(t *testing.T) {
	withTerminal(t, true, func(page *terminalPage) {
		// Every touch event and contextmenu after the page's own listeners ran: none may be cancelled.
		page.Run(`window.cancelled=[];for(const type of ['touchstart','touchmove','touchend','pointerdown','pointerup','contextmenu'])window.addEventListener(type,event=>{if(event.defaultPrevented)window.cancelled.push(type)});`)
		// A program that tracks the mouse: a tap clicks in it, so a long press that ended as a tap would send bytes.
		page.output("\x1b[?1000h\r\nselect-me please\r\n")
		page.Wait("[...document.querySelectorAll('.terminal-row')].some(row=>row.textContent.includes('select-me'))")
		word := `(()=>{const row=[...document.querySelectorAll('.terminal-row')].find(row=>row.textContent.includes('select-me'));
			const walker=document.createTreeWalker(row,NodeFilter.SHOW_TEXT);let node;while((node=walker.nextNode())&&!node.data.includes('select-me'));
			const range=document.createRange();const start=node.data.indexOf('select-me');range.setStart(node,start);range.setEnd(node,start+9);return range;})()`
		at := page.point(`(()=>{const r=` + word + `.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}})()`)
		sent := page.number("window.terminalBytes.length")
		page.touch("touchStart", at)
		time.Sleep(700 * time.Millisecond)
		var held struct {
			Native  bool   `json:"native"`
			Hit     string `json:"hit"`
			Focused bool   `json:"focused"`
		}
		page.eval(`(()=>{const e=document.querySelector('.terminal-input');return {native:e.hasAttribute('data-native-menu'),hit:document.elementFromPoint(`+px(at.X)+`,`+px(at.Y)+`).closest('.terminal-row')?'row':document.elementFromPoint(`+px(at.X)+`,`+px(at.Y)+`).className,focused:document.activeElement===e}})()`, &held)
		// Chrome selects the word under the finger, not in a textarea moved there, and the keyboard stays down.
		if held.Native || held.Hit != "row" || held.Focused {
			t.Fatalf("held = %+v", held)
		}
		// What Chrome's long press does next, which CDP cannot: select the word and send a touch contextmenu.
		selectWord := `(()=>{const selection=getSelection();selection.removeAllRanges();selection.addRange(` + word + `);})()`
		page.Run(selectWord)
		if !page.bool("document.querySelector('.terminal-row').dispatchEvent(new PointerEvent('contextmenu',{pointerType:'touch',bubbles:true,cancelable:true}))") {
			t.Fatal("a touch contextmenu was cancelled")
		}
		page.touch("touchEnd")
		time.Sleep(200 * time.Millisecond)
		if got := page.number("window.terminalBytes.length"); got != sent {
			t.Fatalf("a long press is no tap: %d writes, was %d", got, sent)
		}
		if cancelled := page.text("JSON.stringify(window.cancelled)"); cancelled != "[]" {
			t.Fatalf("cancelled = %s", cancelled)
		}
		// A touch contextmenu is never the program's right button, even with nothing selected; a mouse's is.
		page.Run("getSelection().removeAllRanges()")
		if !page.bool("document.querySelector('.terminal-row').dispatchEvent(new PointerEvent('contextmenu',{pointerType:'touch',bubbles:true,cancelable:true}))") {
			t.Fatal("a touch contextmenu with nothing selected was cancelled")
		}
		if page.bool("document.querySelector('.terminal-row').dispatchEvent(new PointerEvent('contextmenu',{pointerType:'mouse',bubbles:true,cancelable:true}))") {
			t.Fatal("a mouse contextmenu was not the program's")
		}
		page.output("\x1b[?1000l")

		// Output while the word is selected scrolls the program's screen but not the drawn rows.
		page.Run(selectWord)
		time.Sleep(100 * time.Millisecond)
		var flood strings.Builder
		for index := 0; index < 60; index++ {
			flood.WriteString("flood-" + strconv.Itoa(index) + "\r\n")
		}
		page.output(flood.String())
		time.Sleep(300 * time.Millisecond)
		if got := page.text("getSelection().toString()"); got != "select-me" {
			t.Fatalf("selection = %q", got)
		}
		if page.bool("document.querySelector('.terminal-content').textContent.includes('flood-59')") {
			t.Fatal("output was drawn over a selection")
		}
		copied := `(()=>{const data=new DataTransfer();document.querySelector('.terminal-row').dispatchEvent(new ClipboardEvent('copy',{clipboardData:data,bubbles:true,cancelable:true}));return data.getData('text/plain')})()`
		if got := page.text(copied); got != "select-me" {
			t.Fatalf("copy = %q", got)
		}
		// The selection ends (Copy, a tap): what arrived meanwhile is drawn.
		page.Run("getSelection().removeAllRanges()")
		page.Wait("document.querySelector('.terminal-content').textContent.includes('flood-59')")

		// A line longer than the terminal is wide copies as one line; rows lose the blanks that fill them.
		long := "wrap-" + strings.Repeat("a", 120) + "-end"
		page.output("\x1b[2J\x1b[H" + long + "\r\nnext   \r\n")
		page.Wait("document.querySelector('.terminal-content').textContent.includes('next')")
		if wrapped := page.number("[...document.querySelectorAll('.terminal-row')].filter(row=>row.hasAttribute('data-wrapped')).length"); wrapped < 2 {
			t.Fatalf("%d wrapped rows", wrapped)
		}
		page.Run(`(()=>{const rows=[...document.querySelectorAll('.terminal-row')];const last=rows.findIndex(row=>row.textContent.startsWith('next'));
			const range=document.createRange();range.setStart(rows[0],0);range.setEnd(rows[last],rows[last].childNodes.length);
			const selection=getSelection();selection.removeAllRanges();selection.addRange(range);})()`)
		if got := page.text(copied); got != long+"\nnext" {
			t.Fatalf("copy = %q", got)
		}
		page.Run("getSelection().removeAllRanges()")
	})
}

// A terminal whose socket drops gets a new one and keeps working (Terminal.tsx with src/web/reconnect.ts).
func TestADroppedTerminalSocketIsReplacedAndTypingReachesTheShellAgain(t *testing.T) {
	withTerminal(t, false, func(page *terminalPage) {
		prompt := "[...document.querySelectorAll('.terminal-row')].some(row => /[$#]/.test(row.textContent))"
		page.Wait(prompt)
		page.Wait("window.terminalSockets.length === 1 && window.terminalSockets[0].readyState === 1")
		page.Run("window.terminalSockets[0].close()")
		page.Wait("window.terminalSockets.length === 2 && window.terminalSockets[1].readyState === 1")
		page.Wait(prompt)
		page.Run("document.querySelector('.terminal-input').focus()")
		page.call("Input.insertText", map[string]any{"text": "echo reconnected-$((40+2))"})
		page.call("Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13, "text": "\r"})
		page.call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13})
		// Only the shell computes 42: the keys went over the new socket.
		page.Wait("[...document.querySelectorAll('.terminal-row')].some(row => row.textContent.includes('reconnected-42'))")
	})
}

// On Android every key of an on-screen keyboard arrives as keydown 229; the terminal types what changed in its textarea.
func TestTerminalInputChangesOfTheTextareaAsBackspacesAndText(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	type change struct {
		Deleted  int    `json:"deleted"`
		Inserted string `json:"inserted"`
	}
	cases := []struct {
		before, after string
		want          change
	}{
		// Committed text and a composition growing word by word.
		{"", "ls", change{0, "ls"}},
		{"echo d", "echo de", change{0, "e"}},
		// Backspace, inside a composition or through deleteContentBackward.
		{"echo de", "echo d", change{1, ""}},
		// Autocorrect replaces the word before the cursor: retype from the first changed character.
		{"teh", "the ", change{2, "he "}},
		{"git stauts ", "git status ", change{4, "tus "}},
		// Double space becomes a period.
		{"hi  ", "hi. ", change{2, ". "}},
		// Characters, not UTF-16 units: one Backspace for an emoji.
		{"ok 👍", "ok ", change{1, ""}},
		{"a", "a😀b", change{0, "😀b"}},
		// A newline (voice input) is Enter.
		{"", "one\ntwo", change{0, "one\rtwo"}},
		{"x", "x", change{0, ""}},
	}
	pairs := make([][2]string, len(cases))
	for index, each := range cases {
		pairs[index] = [2]string{each.before, each.after}
	}
	input, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", "ui/terminal-input.mjs", string(input))
	command.Env = os.Environ()
	output, err := command.Output()
	if err != nil {
		t.Skipf("node cannot run the vendored TypeScript: %v", err)
	}
	var got []change
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
	for index, each := range cases {
		if !reflect.DeepEqual(got[index], each.want) {
			t.Errorf("typedChange(%q, %q) = %+v, want %+v", each.before, each.after, got[index], each.want)
		}
	}
}
