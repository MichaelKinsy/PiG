package uiviewweb

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/browsertest"
)

// A log, a terminal recording or a shared screen must not hold a live address of the bridge. By default the bridge
// prints the address without its token and writes the full address to a private file; --print-token (or
// UI_VIEW_WEB_PRINT_TOKEN=1) prints the whole address as it always did.

var (
	maskedLine = regexp.MustCompile(`^ui-view-web: (http://127\.0\.0\.1:[0-9]+)/ \(open: (.+[/\\]open\.html)\)\n$`)
	plainLine  = regexp.MustCompile(`^ui-view-web: (http://127\.0\.0\.1:[0-9]+)/\?token=([0-9a-f]{32})\n$`)
)

// syncBuffer is a buffer the bridge's output is written to while a test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// rawBridge is a bridge whose whole output a test keeps. Its temporary directory is a directory of the test's own, so
// what the bridge leaves there is exactly what it made.
type rawBridge struct {
	cmd            *exec.Cmd
	stdout, stderr *syncBuffer
	tmp            string
	done           chan error
	ended          bool
}

// startRaw runs bridge.mjs on any free port, with nodeArgs before the script and env after the inherited environment
// (UI_VIEW_WEB_PRINT_TOKEN and the temporary directory are the test's, not the caller's).
func startRaw(t *testing.T, nodeArgs, env, args []string) *rawBridge {
	t.Helper()
	node := nodeBinary(t)
	r := &rawBridge{stdout: &syncBuffer{}, stderr: &syncBuffer{}, tmp: t.TempDir(), done: make(chan error, 1)}
	r.cmd = exec.Command(node, append(append(nodeArgs, "bridge.mjs", "--port", "0"), args...)...)
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); name != "UI_VIEW_WEB_PRINT_TOKEN" && name != "TMPDIR" && name != "TEMP" && name != "TMP" {
			r.cmd.Env = append(r.cmd.Env, kv)
		}
	}
	r.cmd.Env = append(append(r.cmd.Env, "TMPDIR="+r.tmp, "TEMP="+r.tmp, "TMP="+r.tmp), env...)
	r.cmd.Stdout, r.cmd.Stderr = r.stdout, r.stderr
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { r.done <- r.cmd.Wait() }()
	t.Cleanup(func() {
		if !r.ended {
			_ = r.cmd.Process.Kill()
			<-r.done
		}
	})
	return r
}

// firstLine waits for the bridge's first line of output.
func (r *rawBridge) firstLine(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if out := r.stdout.String(); strings.Contains(out, "\n") {
			return out[:strings.Index(out, "\n")+1]
		}
		select {
		case err := <-r.done:
			// A bridge that printed its line and then ended has its line in stdout by now: it is not "before".
			if out := r.stdout.String(); strings.Contains(out, "\n") {
				r.done <- err
				return out[:strings.Index(out, "\n")+1]
			}
			r.ended = true
			t.Fatalf("the bridge ended before it printed a line: %v\n%s", err, r.stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("the bridge printed no line in 20 s; stderr: %s", r.stderr.String())
	return ""
}

// end signals the bridge and waits for it to end; it returns how it ended.
func (r *rawBridge) end(t *testing.T, sig os.Signal) error {
	t.Helper()
	if err := r.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	return r.wait(t)
}

func (r *rawBridge) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.ended = true
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("the bridge did not end in 20 s")
		return nil
	}
}

// leftovers lists what is in the bridge's temporary directory.
func (r *rawBridge) leftovers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(r.tmp)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// holdsNoToken fails when out holds the token in any form the bridge could print: its hex digits in either case, the
// base64 of its bytes (standard and URL alphabet, padded and not), a prefix or a suffix of it, or a `token=` parameter.
func holdsNoToken(t *testing.T, what, out, token string) {
	t.Helper()
	raw, err := hex.DecodeString(token)
	if err != nil || len(raw) != 16 {
		t.Fatalf("the token %q is not 16 bytes of hex", token)
	}
	forms := map[string]string{
		"hex": token, "HEX": strings.ToUpper(token), "hex prefix": token[:8], "hex suffix": token[len(token)-8:],
		"HEX prefix": strings.ToUpper(token[:8]), "HEX suffix": strings.ToUpper(token[len(token)-8:]),
		"base64": base64.StdEncoding.EncodeToString(raw), "base64url": base64.URLEncoding.EncodeToString(raw),
		"base64 unpadded": base64.RawStdEncoding.EncodeToString(raw), "base64url unpadded": base64.RawURLEncoding.EncodeToString(raw),
		"token=": "token=", "TOKEN=": "TOKEN=",
	}
	for form, needle := range forms {
		if strings.Contains(out, needle) {
			t.Errorf("%s holds the token as %s (%q):\n%s", what, form, needle, out)
		}
	}
}

// tokenFile reads the file `address`, in the directory of the open.html the bridge's line names, and returns the base
// address, the token and the path of that file.
func tokenFile(t *testing.T, line string) (base, token, path string) {
	t.Helper()
	m := maskedLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("the bridge printed %q, want `ui-view-web: http://127.0.0.1:<port>/ (open: <dir>/open.html)`", line)
	}
	base, path = m[1], filepath.Join(filepath.Dir(m[2]), "address")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the file beside the one the line names: %v", err)
	}
	file := regexp.MustCompile(`^` + regexp.QuoteMeta(base) + `/\?token=([0-9a-f]{32})$`).FindStringSubmatch(string(content))
	if file == nil {
		t.Fatalf("the file holds %q, want the full address and nothing else", content)
	}
	return base, file[1], path
}

func statusOf(t *testing.T, url, host string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

// The default line holds no token, in stdout or in stderr, whatever the bridge does with requests, refusals included.
// The page's address with its token is in a private file, and the address in that file opens the page's endpoints.
func TestBridgeDefaultOutputHoldsNoToken(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	line := r.firstLine(t)
	base, token, path := tokenFile(t, line)

	if got := statusOf(t, base+"/", ""); got != http.StatusOK {
		t.Errorf("GET / = %d, want 200", got)
	}
	// Every refusal the bridge makes: no token, a wrong token, a foreign host.
	if got := statusOf(t, base+"/state", ""); got != http.StatusForbidden {
		t.Errorf("GET /state without a token = %d, want 403", got)
	}
	if got := statusOf(t, base+"/state?token=wrong", ""); got != http.StatusForbidden {
		t.Errorf("GET /state with a wrong token = %d, want 403", got)
	}
	if got := statusOf(t, base+"/state?token="+token, "evil.example"); got != http.StatusForbidden {
		t.Errorf("GET /state with a foreign host = %d, want 403", got)
	}
	// The documented way to open the page: the address in the file opens the page, and its token opens the endpoints.
	content, _ := os.ReadFile(path)
	if got := statusOf(t, string(content), ""); got != http.StatusOK {
		t.Errorf("GET of the address in the file = %d, want 200", got)
	}
	if got := statusOf(t, base+"/state?token="+token, ""); got != http.StatusOK {
		t.Errorf("GET /state with the file's token = %d, want 200", got)
	}

	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the token file is %v (%v), want mode 0600", info, err)
		}
		if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("the token file's directory is %v (%v), want mode 0700", info, err)
		}
	}
	if filepath.Dir(filepath.Dir(path)) != r.tmp {
		t.Errorf("the token file %s is not in a directory made in the temporary directory %s", path, r.tmp)
	}

	sig := os.Interrupt
	if runtime.GOOS == "windows" {
		sig = os.Kill
	}
	_ = r.end(t, sig)
	holdsNoToken(t, "stdout", r.stdout.String(), token)
	holdsNoToken(t, "stderr", r.stderr.String(), token)
}

// --print-token and UI_VIEW_WEB_PRINT_TOKEN=1 print the line as the bridge always did and make no file. An empty or
// another value of the variable does not.
func TestBridgePrintTokenRestoresTheLine(t *testing.T) {
	for _, c := range []struct {
		name string
		env  []string
		args []string
	}{
		{name: "flag", args: []string{"--print-token"}},
		{name: "env 1", env: []string{"UI_VIEW_WEB_PRINT_TOKEN=1"}},
		{name: "flag and env", env: []string{"UI_VIEW_WEB_PRINT_TOKEN=1"}, args: []string{"--print-token"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := startRaw(t, nil, c.env, c.args)
			line := r.firstLine(t)
			m := plainLine.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("the bridge printed %q, want `ui-view-web: http://127.0.0.1:<port>/?token=<32 hex digits>` and a newline", line)
			}
			if got := statusOf(t, m[1]+"/state?token="+m[2], ""); got != http.StatusOK {
				t.Errorf("GET /state with the printed token = %d, want 200", got)
			}
			if left := r.leftovers(t); len(left) != 0 {
				t.Errorf("the bridge made %v in its temporary directory, want nothing", left)
			}
			if rest := strings.TrimPrefix(r.stdout.String(), line); rest != "" {
				t.Errorf("the bridge printed more than the line: %q", rest)
			}
		})
	}
	for _, value := range []string{"", "0", "true", "yes", "11", " 1", "1 "} {
		t.Run("env "+value, func(t *testing.T) {
			r := startRaw(t, nil, []string{"UI_VIEW_WEB_PRINT_TOKEN=" + value}, nil)
			_, token, _ := tokenFile(t, r.firstLine(t))
			holdsNoToken(t, "stdout", r.stdout.String(), token)
		})
	}
}

// The token file goes with the bridge on every path the bridge can run code: a signal that ends it and an error that
// nothing catches. What remains in the temporary directory is nothing.
func TestBridgeRemovesTheTokenFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows process takes no signal from another")
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			r := startRaw(t, nil, nil, nil)
			_, _, path := tokenFile(t, r.firstLine(t))
			if err := r.end(t, sig); err != nil {
				t.Errorf("the bridge ended with %v, want 0", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the token file is still there (%v)", err)
			}
			if left := r.leftovers(t); len(left) != 0 {
				t.Errorf("the bridge left %v in its temporary directory", left)
			}
		})
	}
	t.Run("uncaught error", func(t *testing.T) {
		// A script loaded first throws on the turn after the bridge prints its line.
		inject := filepath.Join(t.TempDir(), "inject.mjs")
		script := "const log = console.log;\nconsole.log = (...line) => {\n  log(...line);\n  setImmediate(() => { throw new Error(\"injected\"); });\n};\n"
		if err := os.WriteFile(inject, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		r := startRaw(t, []string{"--import", inject}, nil, nil)
		// The file is gone as soon as the error is thrown, so the line is read and the file is not.
		m := maskedLine.FindStringSubmatch(r.firstLine(t))
		if m == nil {
			t.Fatalf("the bridge printed %q", r.stdout.String())
		}
		path := m[2]
		if err := r.wait(t); err == nil {
			t.Error("the bridge ended with 0 after an uncaught error")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("the token file is still there (%v)", err)
		}
		if left := r.leftovers(t); len(left) != 0 {
			t.Errorf("the bridge left %v in its temporary directory", left)
		}
	})
}

// The line names open.html, and its directory holds the address file and nothing else: an opener that is given the
// address itself would show the token in its argument list and in the browser's, which every user of a Linux machine
// can read; one that is given the path of open.html shows a path.
func TestBridgeWritesAPrivateOpenPage(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	line := r.firstLine(t)
	base, token, path := tokenFile(t, line)
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, " ") != "address open.html" {
		t.Fatalf("the private directory holds %v, want address and open.html", names)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(dir, "open.html")); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("open.html is %v (%v), want mode 0600", info, err)
		}
	}
	page, err := os.ReadFile(filepath.Join(dir, "open.html"))
	if err != nil {
		t.Fatal(err)
	}
	if want := base + "/?token=" + token; !strings.Contains(string(page), want) {
		t.Errorf("open.html does not hold the address %s:\n%s", want, page)
	}
	if !strings.Contains(string(page), "location.replace(") {
		t.Errorf("open.html does not send the browser on to the address:\n%s", page)
	}
	// For a browser that blocks a scripted navigation from a local file: a link to click.
	if !strings.Contains(string(page), `<a href="`+base+"/?token="+token+`" rel="noreferrer">`) {
		t.Errorf("open.html has no link with rel=noreferrer to the address:\n%s", page)
	}
	if !strings.Contains(line, "(open: "+filepath.Join(dir, "open.html")+")") {
		t.Errorf("the line %q does not name %s", line, filepath.Join(dir, "open.html"))
	}
}

// A page of the bridge is not kept and is no referrer, and no answer to a request without the token holds the token.
func TestBridgeSendsNoReferrerAndHoldsNoTokenForAnotherUser(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	base, token, _ := tokenFile(t, r.firstLine(t))
	for _, c := range []struct {
		path   string
		status int
	}{{"/", 200}, {"/client.js", 200}, {"/state", 403}, {"/events", 403}, {"/nothing", 403}, {"/state?token=" + strings.ToUpper(token), 403}, {"/state?token=" + token[:31], 403}, {"/state?token=" + token + "0", 403}} {
		res, err := http.Get(base + c.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != c.status {
			t.Errorf("GET %s = %d, want %d", c.path, res.StatusCode, c.status)
		}
		if got := res.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("GET %s: Referrer-Policy = %q, want no-referrer", c.path, got)
		}
		if got := res.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Errorf("GET %s: Cache-Control = %q, want no-store", c.path, got)
		}
		if strings.Contains(string(body), token) || strings.Contains(res.Header.Get("Location")+res.Header.Get("Set-Cookie"), token) {
			t.Errorf("GET %s answers with the token", c.path)
		}
	}
}

// The bridge gives its token to no process: not in the argument list or the environment of a pig it starts, nor in its
// own. (/proc/<pid>/cmdline is readable by every user; /proc/<pid>/environ by the owner only.)
func TestBridgePassesTheTokenToNoProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	node := nodeBinary(t)
	pig := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(pig, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	b := runBridge(t, node, nil, "--pig", pig)
	b.open(t)
	// The pig it started is running.
	children := 0
	entries, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, path := range entries {
		cmdline, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(cmdline), "sleep\x0060") {
			children++
		}
		for _, file := range []string{path, filepath.Join(filepath.Dir(path), "environ")} {
			if content, err := os.ReadFile(file); err == nil && strings.Contains(string(content), b.token) {
				t.Errorf("%s holds the token", file)
			}
		}
	}
	if children == 0 {
		t.Error("the pig the bridge started was not seen in /proc")
	}
}

// A page opened from open.html draws as one opened from the address, and its address bar does not hold the token, in
// the first load and after a reload.
func TestPageOpensFromTheOpenFileAndKeepsNoTokenInItsAddress(t *testing.T) {
	b := startBridge(t)
	browser := browsertest.Start(t)
	file := filepath.ToSlash(b.openFile)
	if !strings.HasPrefix(file, "/") {
		file = "/" + file
	}
	// browser.Open waits for the load of the document it opened, and open.html sends the browser on before that load.
	if _, err := browser.Call(t.Context(), "Page.navigate", map[string]any{"url": (&url.URL{Scheme: "file", Path: file}).String()}); err != nil {
		t.Fatal(err)
	}
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	if got := browser.String(`location.href`); got != b.base+"/" {
		t.Errorf("the address is %q after the load, want %q", got, b.base+"/")
	}
	browser.Run(`window.beforeReload = true; location.reload()`)
	browser.Wait(`!window.beforeReload && window.uiView && window.uiView.attachment === 1`)
	if got := browser.String(`location.href`); got != b.base+"/" {
		t.Errorf("the address is %q after a reload, want %q", got, b.base+"/")
	}
	// The tab's first blank page and the page itself: open.html was replaced, not kept.
	if got := browser.String(`String(history.length)`); got != "2" {
		t.Errorf("the history holds %s entries, want 2", got)
	}
}
