// Package browsertest is a real browser for the tests of the web surface (Pi's test/node/web/browser.ts): headless Chromium
// driven over the DevTools protocol on one page. The binary is `CHROMIUM_BIN`, else `chromium-docker` on the PATH (a wrapper
// running the Playwright image's Chromium); a test that needs one is skipped without either (Binary).
package browsertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Binary is the Chromium binary, or "" when there is none: `CHROMIUM_BIN`, else `chromium-docker` on the PATH, whichever answers
// `--version`.
func Binary(ctx context.Context) string {
	for _, candidate := range []string{os.Getenv("CHROMIUM_BIN"), "chromium-docker"} {
		if candidate == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		//nolint:gosec // G204: the binary is the test runner's own choice (CHROMIUM_BIN), as Pi's driver runs it
		err := exec.CommandContext(ctx, candidate, "--version").Run()
		cancel()
		if err == nil {
			return candidate
		}
	}
	return ""
}

// pollEvery is how often a condition of the page is looked at.
const pollEvery = 50 * time.Millisecond

// Poll calls check until it is true (timeout at most); an error from it counts as "not yet".
func Poll(ctx context.Context, timeout time.Duration, what string, check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		if ok, err := check(ctx); err == nil && ok {
			return nil
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s", what)
		}
	}
}

// answer is a DevTools call's reply.
type answer struct {
	result json.RawMessage
	err    error
}

// Browser is one headless Chromium with one page.
type Browser struct {
	t       testing.TB
	command *exec.Cmd
	exited  chan struct{}
	profile string
	socket  *websocket.Conn

	mu        sync.Mutex
	writeMu   sync.Mutex
	id        int
	pending   map[int]chan answer
	loaded    map[string]bool
	listeners map[string]func(params json.RawMessage)
	console   []string
	closed    bool
	done      chan struct{}
	// stderr is the end of what Chromium wrote to its standard error, logged when the test failed.
	stderr *tail
	// base is the context of the driver's own calls: the test's, kept alive past its end for the cleanup that closes Chromium.
	base context.Context
}

// tail keeps the last 8 KiB written to it.
type tail struct {
	mu   sync.Mutex
	data []byte
}

func (w *tail) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	if len(w.data) > 8192 {
		w.data = w.data[len(w.data)-8192:]
	}
	return len(p), nil
}

func (w *tail) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.data)
}

// Start starts Chromium (the test is skipped when there is none) and attaches to its page; it is closed when the test ends.
func Start(t testing.TB) *Browser {
	t.Helper()
	binary := Binary(t.Context())
	if binary == "" {
		// CI sets CHORD_REQUIRE_CHROMIUM: a missing browser fails the tests there rather than skipping them (Pi's web tests run).
		if os.Getenv("CHORD_REQUIRE_CHROMIUM") != "" {
			t.Fatal("no Chromium, and CHORD_REQUIRE_CHROMIUM is set: set CHROMIUM_BIN (or put chromium-docker on the PATH)")
		}
		t.Skip("no Chromium: set CHROMIUM_BIN (or put chromium-docker on the PATH)")
	}
	profile, err := os.MkdirTemp("", "chord-browser-")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--headless=new", "--no-sandbox", "--no-zygote", "--disable-gpu", "--disable-dev-shm-usage",
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank",
	}
	command := exec.Command(binary, args...) //nolint:gosec // G204: the binary is the test runner's own choice (CHROMIUM_BIN)
	stderr := &tail{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(profile)
		t.Fatal(err)
	}
	b := &Browser{
		stderr: stderr, t: t, base: context.WithoutCancel(t.Context()), command: command, exited: make(chan struct{}), profile: profile, pending: map[int]chan answer{},
		loaded: map[string]bool{}, listeners: map[string]func(json.RawMessage){}, done: make(chan struct{}),
	}
	go func() {
		_ = command.Wait()
		close(b.exited)
	}()
	t.Cleanup(b.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	portFile := filepath.Join(profile, "DevToolsActivePort")
	var port string
	err = Poll(ctx, 60*time.Second, "Chromium's port", func(context.Context) (bool, error) {
		text, err := os.ReadFile(portFile)
		if err != nil || !strings.Contains(string(text), "\n") {
			return false, err
		}
		port = strings.SplitN(string(text), "\n", 2)[0]
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &targets); err != nil || len(targets) == 0 {
		t.Fatalf("Chromium's targets: %s (%v)", body, err)
	}
	target := targets[0]
	for _, each := range targets {
		if each.Type == "page" {
			target = each
			break
		}
	}
	socket, _, err := websocket.DefaultDialer.DialContext(ctx, target.WebSocketDebuggerURL, nil) //nolint:bodyclose // gorilla/websocket hands back a handshake response whose body is already read and closed
	if err != nil {
		t.Fatal(err)
	}
	socket.SetReadLimit(64 << 20)
	b.socket = socket
	//portlint:allow golifetime read ends when the socket closes (Close closes it and waits on done)
	go b.read()
	for _, method := range []string{"Page.enable", "Runtime.enable"} {
		if _, err := b.Call(ctx, method, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Call(ctx, "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	return b
}

// read dispatches the socket's messages until it closes.
func (b *Browser) read() {
	defer close(b.done)
	for {
		_, raw, err := b.socket.ReadMessage()
		if err != nil {
			b.mu.Lock()
			for id, waiting := range b.pending {
				waiting <- answer{err: errors.New("the browser's socket closed")}
				delete(b.pending, id)
			}
			b.mu.Unlock()
			return
		}
		var message struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(raw, &message) != nil {
			continue
		}
		b.mu.Lock()
		if message.Method != "" {
			b.event(message.Method, message.Params)
			listener := b.listeners[message.Method]
			b.mu.Unlock()
			if listener != nil {
				listener(message.Params)
			}
			continue
		}
		waiting := b.pending[message.ID]
		delete(b.pending, message.ID)
		b.mu.Unlock()
		if waiting == nil {
			continue
		}
		if message.Error != nil {
			waiting <- answer{err: errors.New(message.Error.Message)}
		} else {
			waiting <- answer{result: message.Result}
		}
	}
}

// event records what the driver itself needs of an event (b.mu held): documents loaded and the console.
func (b *Browser) event(method string, params json.RawMessage) {
	switch method {
	case "Page.lifecycleEvent":
		var event struct{ Name, LoaderID string }
		var raw struct {
			Name     string `json:"name"`
			LoaderID string `json:"loaderId"`
		}
		_ = json.Unmarshal(params, &raw)
		event.Name, event.LoaderID = raw.Name, raw.LoaderID
		if event.Name == "DOMContentLoaded" {
			b.loaded[event.LoaderID] = true
		}
	case "Runtime.consoleAPICalled":
		var event struct {
			Args []struct {
				Value any `json:"value"`
			} `json:"args"`
		}
		_ = json.Unmarshal(params, &event)
		parts := make([]string, len(event.Args))
		for i, arg := range event.Args {
			parts[i] = fmt.Sprint(arg.Value)
		}
		b.console = append(b.console, strings.Join(parts, " "))
	case "Runtime.exceptionThrown":
		var event struct {
			ExceptionDetails struct {
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		_ = json.Unmarshal(params, &event)
		description := event.ExceptionDetails.Exception.Description
		if description == "" {
			description = "unknown"
		}
		b.console = append(b.console, "exception: "+description)
	}
}

// Console is the page's console messages so far (an uncaught exception as `exception: <description>`).
func (b *Browser) Console() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.console...)
}

// On calls listener with the params of each DevTools event method (one listener per method; nil removes it). It runs on the
// driver's reading goroutine: it must not call the browser.
func (b *Browser) On(method string, listener func(params json.RawMessage)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if listener == nil {
		delete(b.listeners, method)
	} else {
		b.listeners[method] = listener
	}
}

// Call is one DevTools call.
func (b *Browser) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if params == nil {
		params = map[string]any{}
	}
	waiting := make(chan answer, 1)
	b.mu.Lock()
	b.id++
	id := b.id
	b.pending[id] = waiting
	b.mu.Unlock()
	//portlint:allow mapkeyorder a DevTools message is a JSON object whose keys the browser reads by name, in any order
	message, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	b.writeMu.Lock()
	err = b.socket.WriteMessage(websocket.TextMessage, message)
	b.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case got := <-waiting:
		return got.result, got.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// Evaluate is the value of expression in the page (awaited), as its JSON (empty for undefined); callers decode it into their own type.
func (b *Browser) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	raw, err := b.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true})
	if err != nil {
		return nil, err
	}
	var got struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, err
	}
	if len(got.ExceptionDetails) > 0 {
		return nil, fmt.Errorf("evaluating %s: %s", expression, got.ExceptionDetails)
	}
	return got.Result.Value, nil
}

// String is expression's value as a string; the test fails when it is not one.
func (b *Browser) String(expression string) string {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(b.base, 30*time.Second)
	defer cancel()
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		b.t.Fatal(err)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		b.t.Fatalf("%s is not a string: %s (%v)", expression, raw, err)
	}
	return value
}

// Run evaluates expression for its effect; the test fails when it throws.
func (b *Browser) Run(expression string) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(b.base, 30*time.Second)
	defer cancel()
	if _, err := b.Evaluate(ctx, expression); err != nil {
		b.t.Fatal(err)
	}
}

// Wait waits until expression is truthy in the page (30 s at most, or timeout); the test fails otherwise, with the page's console.
func (b *Browser) Wait(expression string, timeout ...time.Duration) {
	b.t.Helper()
	limit := 30 * time.Second
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	err := Poll(b.base, limit, expression, func(ctx context.Context) (bool, error) {
		raw, err := b.Evaluate(ctx, "Boolean("+expression+")")
		if err != nil {
			return false, err
		}
		var ok bool
		return ok, json.Unmarshal(raw, &ok)
	})
	if err != nil {
		b.t.Fatalf("%v (console: %q)", err, b.Console())
	}
}

// Open navigates to url and waits for its document.
func (b *Browser) Open(url string) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(b.base, 60*time.Second)
	defer cancel()
	raw, err := b.Call(ctx, "Page.navigate", map[string]any{"url": url})
	if err != nil {
		b.t.Fatal(err)
	}
	var result struct {
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	_ = json.Unmarshal(raw, &result)
	if result.ErrorText != "" {
		b.t.Fatalf("opening %s: %s", url, result.ErrorText)
	}
	if result.LoaderID == "" {
		return
	}
	err = Poll(ctx, 30*time.Second, "the document", func(context.Context) (bool, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.loaded[result.LoaderID], nil
	})
	if err != nil {
		b.t.Fatal(err)
	}
}

// Type types text into the focused element.
func (b *Browser) Type(text string) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(b.base, 30*time.Second)
	defer cancel()
	if _, err := b.Call(ctx, "Input.insertText", map[string]any{"text": text}); err != nil {
		b.t.Fatal(err)
	}
}

// Close closes Chromium and removes its profile; it is also what the test's cleanup runs.
func (b *Browser) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	if b.socket == nil {
		// Start failed before the connection was made: nothing to ask, and no reader to wait for.
		_ = b.command.Process.Kill()
		<-b.exited
		_ = os.RemoveAll(b.profile)
		return
	}
	ctx, cancel := context.WithTimeout(b.base, 10*time.Second)
	_, _ = b.Call(ctx, "Browser.close", nil)
	cancel()
	_ = b.socket.Close()
	<-b.done
	select {
	case <-b.exited:
	case <-time.After(10 * time.Second):
		_ = b.command.Process.Kill()
		<-b.exited
	}
	if b.t.Failed() {
		b.t.Logf("Chromium's standard error: %s", b.stderr)
	}
	_ = os.RemoveAll(b.profile)
}

// Emulate sets the media feature (`prefers-color-scheme`) to value for the page.
func (b *Browser) Emulate(feature, value string) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(b.base, 30*time.Second)
	defer cancel()
	features := []map[string]string{{"name": feature, "value": value}}
	if _, err := b.Call(ctx, "Emulation.setEmulatedMedia", map[string]any{"features": features}); err != nil {
		b.t.Fatal(err)
	}
}
