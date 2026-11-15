//go:build integration && (linux || darwin)

package integration

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/browsertest"
)

// pig additive (D614): the reference web client (examples/ui-view-web) as a client of a real interactive session. The
// bridge runs with --connect on the session's view-endpoint directory, and a page in headless Chromium is its client
// beside the terminal that tmux drives.

// uiViewWebBridge runs the reference bridge connected to the run's view endpoint and returns the page's address. It
// passes --print-token, so the line it parses is the whole address; the default, a file, is the examples' own tests'.
func uiViewWebBridge(t *testing.T, node string, run *uiViewRun) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join(root, "examples", "ui-view-web", "bridge.mjs"), "--port", "0", "--print-token", "--connect", filepath.Join(run.home, "agent", "view-endpoint"))
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("the bridge printed no address: %v", err)
	}
	address := strings.TrimSpace(strings.TrimPrefix(line, "ui-view-web: "))
	if !strings.HasPrefix(address, "http://127.0.0.1:") {
		t.Fatalf("the bridge printed %q", line)
	}
	return address
}

// One session, its terminal and a web page. The page draws the mounts the terminal draws; a click in the page changes
// what the terminal shows and a key in the terminal changes the page. A reload keeps the page's connection. A new
// session closes it: the page attaches to the session that follows and draws what that one mounts. Each time the page
// holds what a client of the socket with the page's abilities is sent, by digest. The exit closes the connection for
// good: the page shows no mount and says that it waits for a session.
func TestUIViewWebPageAndTerminalShareOneSession(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	browser := browsertest.Start(t)
	run := startUIViewAt(t, uiViewShortHome(t), "shared.yaml", "fullscreen", true)
	uiViewSocket(t, run)
	browser.Open(uiViewWebBridge(t, node, run))
	// What the terminal and the page show when the test fails.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		page, err := browser.Evaluate(context.WithoutCancel(t.Context()), `JSON.stringify({ session: window.uiView?.session, attachment: window.uiView?.attachment, state: document.getElementById("state")?.textContent, mounts: [...document.querySelectorAll(".mount")].map((m) => m.dataset.key + ": " + m.textContent), lines: [...document.querySelectorAll(".line")].map((line) => line.textContent) })`)
		t.Logf("the terminal:\n%s\nthe page: %s %v", run.capture(), page, err)
	})
	browser.Wait(`window.uiView && window.uiView.attachment > 0`)
	abilities := browser.String(`JSON.stringify(window.uiView.abilities)`)

	// holds returns the mounts on the page, each with the digest it was drawn from, and the mounts the session sends a
	// client of the socket that has the page's abilities.
	holds := func() (page, session string) {
		t.Helper()
		wire := uiViewDial(t, run)
		defer func() { _ = wire.c.Close() }()
		wire.send(`{"id":"attach","type":"ui_view_attach","abilities":` + abilities + `}`)
		reply, line := wire.until("response", `"command":"ui_view_attach"`)
		if reply["success"] != true {
			t.Fatalf("attach with the page's abilities = %s", line)
		}
		var sent []string
		for _, s := range reply["data"].(map[string]any)["snapshots"].([]any) {
			snapshot := s.(map[string]any)
			digest, _ := snapshot["digest"].(string)
			if state := snapshot["state"]; state == "live" || state == "hidden" {
				sent = append(sent, snapshot["mount"].(map[string]any)["key"].(string)+" "+digest)
			}
		}
		sort.Strings(sent)
		return browser.String(`[...document.querySelectorAll(".mount")].map((m) => m.dataset.key + " " + m.dataset.digest).sort().join("\n")`), strings.Join(sent, "\n")
	}
	// shows waits until the terminal and the page's panel have the title, and compares what the page holds with what
	// the session has: the panel and the widget, each once, by digest.
	shows := func(title string) {
		t.Helper()
		run.settle(title, "fixture widget row")
		browser.Wait(`document.querySelectorAll(".mount").length === 2 && document.querySelector('[data-key="panel"] .text').textContent === "` + title + `" && document.querySelector('[data-key="note"] .text').textContent === "fixture widget row"`)
		page, session := holds()
		if page != session || strings.Count(page, "\n") != 1 || strings.Contains(page+"\n", " \n") {
			t.Fatalf("the page shows\n%s\nthe session has\n%s", page, session)
		}
	}
	// click picks an item of the panel's list in the page.
	click := func(label string) {
		t.Helper()
		browser.Run(`[...document.querySelectorAll('[data-key="panel"] [role=option]')].find((option) => option.textContent.includes("` + label + `")).click()`)
	}

	run.send("/vmount\r")
	shows("fixture panel")
	// The page cannot prompt over the view endpoint: its prompt is gone and one line says why.
	if got := browser.String(`[getComputedStyle(document.getElementById("prompt")).display, document.getElementById("attached").hidden].join()`); got != "none,false" {
		t.Errorf("the prompt of the connected page: %s", got)
	}
	// A click in the page changes what the terminal shows.
	click("Alpha item")
	shows("picked alpha")
	browser.Wait(`document.querySelector('[data-key="panel"] footer').dataset.outcome === "done"`)
	// A key in the terminal changes the page.
	run.sendKey("Down")
	run.sendKey("Enter")
	shows("picked beta")

	// A reload keeps the registry, so the session closes nothing: the page keeps its connection and its attachment.
	// The reload replaces the fixture's instance, whose mounts end: the page is sent them broken and shows none, as
	// the session holds none, until the new instance mounts again.
	session := browser.String(`window.uiView.session + " " + window.uiView.attachment`)
	// The focus is in the panel's list: Escape returns it to the editor, so the next command is typed there.
	run.sendKey("Escape")
	run.settle()
	run.send("/reload\r")
	run.settle("Reloaded keybindings")
	var page, has string
	if !waitFor(30*time.Second, func() bool {
		page, has = holds()
		return page == has
	}) {
		t.Fatalf("after the reload the page shows\n%s\nthe session has\n%s", page, has)
	}
	if got := browser.String(`window.uiView.session + " " + window.uiView.attachment`); got != session {
		t.Fatalf("after the reload the page has the session and attachment %q, it had %q", got, session)
	}
	browser.Wait(`document.querySelectorAll(".mount").length === 0`)
	run.send("/vmount\r")
	shows("fixture panel")
	run.sendKey("Escape")
	run.settle()

	// A new session ends the registry: the session closes the connection, the bridge connects to the socket of the
	// session that follows, and the page attaches to it. Nothing of the session before is on the page.
	run.send("/new\r")
	browser.Wait(`window.uiView.session !== "" && !"` + session + `".startsWith(window.uiView.session + " ") && window.uiView.attachment > 0 && document.querySelectorAll(".mount").length === 0`)
	run.settle()
	run.send("/vmount\r")
	shows("fixture panel")
	click("Alpha item")
	shows("picked alpha")
	if got := browser.String(`String(window.uiView.resyncs) + "/" + String(window.uiView.gaps)`); got != "0/0" {
		t.Errorf("resyncs/gaps = %s, want 0/0", got)
	}

	// The exit closes the connection and removes the socket: nothing of the session is on the page, and the page says
	// that it waits for a session.
	path, _ := uiViewSocket(t, run)
	run.sendKey("Escape")
	run.settle()
	run.send("/quit\r")
	browser.Wait(`document.querySelectorAll(".mount").length === 0 && window.uiView.session === "" && document.getElementById("state").textContent.includes("no running session")`)
	if !waitFor(30*time.Second, func() bool {
		_, err := os.Lstat(path)
		return err != nil
	}) {
		t.Fatalf("the session ended and left its socket %s", path)
	}
}
