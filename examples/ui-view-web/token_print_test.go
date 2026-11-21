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

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// A log, a terminal recording or a shared screen must not hold a live address of the bridge. By default the bridge
// prints the address without its code and writes the full address to a private file; --print-token (or
// UI_VIEW_WEB_PRINT_TOKEN=1) prints the whole address, which is good for one use.

var (
	maskedLine = regexp.MustCompile(`^ui-view-web: (http://127\.0\.0\.1:[0-9]+)/ \(open: (.+[/\\]open\.html)\)\n$`)
	plainLine  = regexp.MustCompile(`^ui-view-web: (http://127\.0\.0\.1:[0-9]+)/#([0-9a-f]{32})\n$`)
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

// holdsNoSecret fails when out holds a code or a secret (given in hex) in any form the bridge or a page could write: its
// hex digits in either case, the base64 of its bytes (standard and URL alphabet, padded and not, and each percent-encoded
// as it would be in an address), a prefix or a suffix of it, or a `token=` parameter.
func holdsNoSecret(t *testing.T, what, out, secret string) {
	t.Helper()
	raw, err := hex.DecodeString(secret)
	if err != nil || len(raw) < 16 {
		t.Fatalf("the secret %q is not at least 16 bytes of hex", secret)
	}
	std := base64.StdEncoding.EncodeToString(raw)
	forms := map[string]string{
		"hex": secret, "HEX": strings.ToUpper(secret), "hex prefix": secret[:8], "hex suffix": secret[len(secret)-8:],
		"HEX prefix": strings.ToUpper(secret[:8]), "HEX suffix": strings.ToUpper(secret[len(secret)-8:]),
		"base64": std, "base64url": base64.URLEncoding.EncodeToString(raw),
		"base64 unpadded": base64.RawStdEncoding.EncodeToString(raw), "base64url unpadded": base64.RawURLEncoding.EncodeToString(raw),
		"base64 percent-encoded": url.QueryEscape(std), "base64 path-escaped": url.PathEscape(std),
		"token=": "token=", "TOKEN=": "TOKEN=",
	}
	for form, needle := range forms {
		if strings.Contains(out, needle) {
			t.Errorf("%s holds the secret as %s (%q):\n%s", what, form, needle, out)
		}
	}
}

func statusOf(t *testing.T, url, host string, header ...string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
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

func endSignal() os.Signal {
	if runtime.GOOS == "windows" {
		return os.Kill
	}
	return os.Interrupt
}

// The default line holds no code, in stdout or in stderr, whatever the bridge does with requests, refusals included.
// The page's address with its code is in a private file, and the code in that file opens the page's endpoints once.
func TestBridgeDefaultOutputHoldsNoToken(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	line := r.firstLine(t)
	b := bridgeFrom(t, line)
	first := b.code(t)
	path := b.addressFile()
	// Signing in with the code makes the next one: the file is checked below as the bridge leaves it after that.
	auth := b.auth(t)
	secret := strings.TrimPrefix(auth, "Bearer ")

	if got := statusOf(t, b.base+"/", ""); got != http.StatusOK {
		t.Errorf("GET / = %d, want 200", got)
	}
	// Every refusal the bridge makes: no secret, a wrong secret, a foreign host.
	if got := statusOf(t, b.base+"/state", ""); got != http.StatusForbidden {
		t.Errorf("GET /state without a secret = %d, want 403", got)
	}
	if got := statusOf(t, b.base+"/state", "", "Authorization", "Bearer "+strings.Repeat("0", 64)); got != http.StatusForbidden {
		t.Errorf("GET /state with a wrong secret = %d, want 403", got)
	}
	if got := statusOf(t, b.base+"/state", "evil.example", "Authorization", auth); got != http.StatusForbidden {
		t.Errorf("GET /state with a foreign host = %d, want 403", got)
	}
	// The documented way to open the page: the address in the file opens the page, and its code opens the endpoints once.
	content, _ := os.ReadFile(path)
	if got := statusOf(t, string(content), ""); got != http.StatusOK {
		t.Errorf("GET of the address in the file = %d, want 200", got)
	}
	if got := statusOf(t, b.base+"/state", "", "Authorization", auth); got != http.StatusOK {
		t.Errorf("GET /state with the signed-in secret = %d, want 200", got)
	}

	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the address file is %v (%v), want mode 0600", info, err)
		}
		if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("the address file's directory is %v (%v), want mode 0700", info, err)
		}
	}
	if filepath.Dir(filepath.Dir(path)) != r.tmp {
		t.Errorf("the address file %s is not in a directory made in the temporary directory %s", path, r.tmp)
	}
	next := b.code(t)

	_ = r.end(t, endSignal())
	for _, secret := range []string{first, next, secret} {
		holdsNoSecret(t, "stdout", r.stdout.String(), secret)
		holdsNoSecret(t, "stderr", r.stderr.String(), secret)
	}
}

// --print-token and UI_VIEW_WEB_PRINT_TOKEN=1 print one line, `ui-view-web: http://127.0.0.1:<port>/#<code>`, and make no
// file. The code in it is good for one exchange and ten minutes; the bridge prints no second line and makes no next code.
// An empty or another value of the variable does not print it.
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
				t.Fatalf("the bridge printed %q, want `ui-view-web: http://127.0.0.1:<port>/#<32 hex digits>` and a newline", line)
			}
			b := bridge{base: m[1], signIn: &signIn{}}
			status, body := b.exchange(t, m[2])
			if status != http.StatusOK {
				t.Fatalf("POST /exchange with the printed code = %d %s, want 200", status, body)
			}
			if got := statusOf(t, m[1]+"/state", "", "Authorization", "Bearer "+secretIn(t, body)); got != http.StatusOK {
				t.Errorf("GET /state with the secret the printed code gave = %d, want 200", got)
			}
			if status, _ := b.exchange(t, m[2]); status != http.StatusForbidden {
				t.Errorf("the printed code a second time = %d, want 403: it is good for one exchange", status)
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
			b := bridgeFrom(t, r.firstLine(t))
			holdsNoSecret(t, "stdout", r.stdout.String(), b.code(t))
		})
	}
}

// The address file goes with the bridge on every path the bridge can run code: a signal that ends it and an error that
// nothing catches, also after the codes were used and the files written again. What remains in the temporary directory
// is nothing.
func TestBridgeRemovesTheTokenFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows process takes no signal from another")
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			r := startRaw(t, nil, nil, nil)
			b := bridgeFrom(t, r.firstLine(t))
			path := b.addressFile()
			b.auth(t)
			if err := r.end(t, sig); err != nil {
				t.Errorf("the bridge ended with %v, want 0", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the address file is still there (%v)", err)
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
		r := startRaw(t, []string{"--import", nodeurl.PathToFileURL(inject, runtime.GOOS == "windows")}, nil, nil)
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
			t.Errorf("the address file is still there (%v)", err)
		}
		if left := r.leftovers(t); len(left) != 0 {
			t.Errorf("the bridge left %v in its temporary directory", left)
		}
	})
}

// The line names open.html, and its directory holds the address file and nothing else: an opener that is given the
// address itself would show the code in its argument list and in the browser's, which every user of a Linux machine
// can read; one that is given the path of open.html shows a path.
func TestBridgeWritesAPrivateOpenPage(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	line := r.firstLine(t)
	b := bridgeFrom(t, line)
	code := b.code(t)
	dir := filepath.Dir(b.addressFile())
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
	if want := b.base + "/#" + code; !strings.Contains(string(page), want) {
		t.Errorf("open.html does not hold the address %s:\n%s", want, page)
	}
	if !strings.Contains(string(page), "location.replace(") {
		t.Errorf("open.html does not send the browser on to the address:\n%s", page)
	}
	// For a browser that blocks a scripted navigation from a local file: a link to click.
	if !strings.Contains(string(page), `<a href="`+b.base+"/#"+code+`" rel="noreferrer">`) {
		t.Errorf("open.html has no link with rel=noreferrer to the address:\n%s", page)
	}
	if !strings.Contains(line, "(open: "+filepath.Join(dir, "open.html")+")") {
		t.Errorf("the line %q does not name %s", line, filepath.Join(dir, "open.html"))
	}
}

// A page of the bridge is not kept and is no referrer, and no answer to a request without the secret holds the code or
// the secret, or sets a cookie: the bridge keeps nothing in the browser that another port of the host could be sent.
func TestBridgeSendsNoReferrerAndHoldsNoTokenForAnotherUser(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	b := bridgeFrom(t, r.firstLine(t))
	code := b.code(t)
	auth := b.auth(t)
	secret := strings.TrimPrefix(auth, "Bearer ")
	bearer := func(v string) []string { return []string{"Authorization", v} }
	for _, c := range []struct {
		path   string
		header []string
		status int
	}{
		{"/", nil, 200}, {"/client.js", nil, 200}, {"/state", nil, 403}, {"/events", nil, 403}, {"/nothing", nil, 403}, {"/exchange", nil, 403},
		{"/state?token=" + secret, nil, 403}, {"/state?secret=" + secret, nil, 403}, {"/events?token=" + secret, nil, 403},
		{"/state", bearer("Bearer " + strings.ToUpper(secret)), 403}, {"/state", bearer("Bearer " + secret[:63]), 403},
		{"/state", bearer("Bearer " + secret + "0"), 403}, {"/state", bearer(secret), 403}, {"/state", bearer("Bearer " + code), 403},
		{"/state", bearer("Bearer "), 403}, {"/state", bearer("Basic " + secret), 403},
	} {
		req, _ := http.NewRequest(http.MethodGet, b.base+c.path, nil)
		for i := 0; i+1 < len(c.header); i += 2 {
			req.Header.Set(c.header[i], c.header[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		what := c.path + " " + strings.Join(c.header, "=")
		if res.StatusCode != c.status {
			t.Errorf("GET %s = %d, want %d", what, res.StatusCode, c.status)
		}
		if got := res.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("GET %s: Referrer-Policy = %q, want no-referrer", what, got)
		}
		if got := res.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Errorf("GET %s: Cache-Control = %q, want no-store", what, got)
		}
		if res.StatusCode == 403 {
			for _, held := range []string{code, secret} {
				if strings.Contains(string(body), held) || strings.Contains(res.Header.Get("Location"), held) {
					t.Errorf("GET %s answers with a secret", what)
				}
			}
		}
		if got := res.Header.Get("Set-Cookie"); got != "" {
			t.Errorf("GET %s sets the cookie %q, want none", what, got)
		}
	}
}

// The bridge gives its code and its secrets to no process: not in the argument list or the environment of a pig it
// starts, nor in its own. (/proc/<pid>/cmdline is readable by every user; /proc/<pid>/environ by the owner only.)
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
	used := b.code(t)
	b.open(t)
	secret := strings.TrimPrefix(b.auth(t), "Bearer ")
	next := b.code(t)
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
			content, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			for _, held := range []string{used, next, secret} {
				if strings.Contains(string(content), held) {
					t.Errorf("%s holds a code or a secret of the bridge", file)
				}
			}
		}
	}
	if children == 0 {
		t.Error("the pig the bridge started was not seen in /proc")
	}
}

// A page opened from open.html draws as one opened from the address, and its address bar does not hold the code, in
// the first load and after a reload.
func TestPageOpensFromTheOpenFileAndKeepsNoTokenInItsAddress(t *testing.T) {
	b := startBridge(t)
	browser := browsertest.Start(t)
	b.openFileIn(t, browser)
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
