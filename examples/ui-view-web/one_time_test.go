package uiviewweb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/browsertest"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// The address of the bridge is good for one load. It holds a one-time code in its fragment; the page posts the code to
// /exchange and is given a session secret in the answer, keeps it in sessionStorage and sends it in an Authorization
// header. No request URL holds the code or the secret, and the bridge sets no cookie.

// The three things the page says when it is not signed in, as the README quotes them.
const (
	msgUsed      = "This address was used or has expired. Open the file the bridge named again; if there is none, restart the bridge."
	msgRestarted = "The bridge does not know this tab any more: it was restarted. Open the file the new bridge named."
	msgSignedOut = "This tab is not signed in. Open the file the bridge named."
)

// maxSecrets is how many session secrets the bridge keeps: the newest, and the oldest is dropped.
const maxSecrets = 64

// signIn is the session secret the tests of one bridge share: the first call of auth signs in, the others reuse it.
type signIn struct {
	once   sync.Once
	header string
}

var secretForm = regexp.MustCompile(`^[0-9a-f]{64}$`)

// bridgeFrom is the bridge whose first line is line: its base address and the open.html the line names.
func bridgeFrom(t *testing.T, line string) bridge {
	t.Helper()
	m := maskedLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("the bridge printed %q, want `ui-view-web: http://127.0.0.1:<port>/ (open: <dir>/open.html)`", line)
	}
	return bridge{base: m[1], openFile: m[2], signIn: &signIn{}}
}

// addressFile is the file beside open.html that holds the address with the live code.
func (b bridge) addressFile() string { return filepath.Join(filepath.Dir(b.openFile), "address") }

// code reads the live one-time code from the address file, as an author's script does.
func (b bridge) code(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(b.addressFile())
	if err != nil {
		t.Fatalf("the file beside open.html: %v", err)
	}
	m := regexp.MustCompile(`^` + regexp.QuoteMeta(b.base) + `/#([0-9a-f]{32})$`).FindStringSubmatch(string(content))
	if m == nil {
		t.Fatalf("the address file holds %q, want `%s/#<32 hex digits>` and nothing else", content, b.base)
	}
	return m[1]
}

// post sends body to path as a client that is not the page: it may name another host and carry other headers.
func (b bridge) post(path, body, host string, header ...string) (int, string, http.Header, error) {
	req, err := http.NewRequest(http.MethodPost, b.base+path, strings.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = res.Body.Close() }()
	text, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, string(text), res.Header, err
}

func codeBody(code string) string {
	text, _ := json.Marshal(map[string]string{"code": code})
	return string(text)
}

// exchange posts code to /exchange as the page does and returns the status and the body.
func (b bridge) exchange(t *testing.T, code string, header ...string) (int, string) {
	t.Helper()
	status, body, _, err := b.post("/exchange", codeBody(code), "", header...)
	if err != nil {
		t.Fatal(err)
	}
	return status, body
}

// secretIn is the secret of a successful /exchange answer.
func secretIn(t *testing.T, body string) string {
	t.Helper()
	var answer struct{ Secret string }
	if err := json.Unmarshal([]byte(body), &answer); err != nil || !secretForm.MatchString(answer.Secret) {
		t.Fatalf("the exchange answered %q, want {\"secret\":\"<64 hex digits>\"}", body)
	}
	return answer.Secret
}

// auth is the Authorization header every test that is not the page sends, and the one place a test gets it: the first
// call exchanges the live code, the rest reuse the secret. The bridge has written the next code by then, so a browser
// that opens the bridge afterwards (openIn) is given a live one.
func (b bridge) auth(t *testing.T) string {
	t.Helper()
	b.signIn.once.Do(func() {
		status, body := b.exchange(t, b.code(t))
		if status != http.StatusOK {
			t.Fatalf("POST /exchange with the live code = %d %s, want 200", status, body)
		}
		b.signIn.header = "Bearer " + secretIn(t, body)
	})
	return b.signIn.header
}

// openIn opens the bridge's page in the browser with the live code, as the file the bridge names does.
func (b bridge) openIn(t *testing.T, browser *browsertest.Browser, query string) {
	t.Helper()
	browser.Open(b.base + "/" + query + "#" + b.code(t))
}

// openFileIn opens open.html, the way the README tells an author to.
func (b bridge) openFileIn(t *testing.T, browser *browsertest.Browser) {
	t.Helper()
	file := filepath.ToSlash(b.openFile)
	if !strings.HasPrefix(file, "/") {
		file = "/" + file
	}
	// browser.Open waits for the load of the document it opened, and open.html sends the browser on before that load.
	if _, err := browser.Call(t.Context(), "Page.navigate", map[string]any{"url": (&url.URL{Scheme: "file", Path: file}).String()}); err != nil {
		t.Fatal(err)
	}
}

// hookScript is loaded before the bridge (node --import). Each part is switched on by the environment: HOOK_SKEW_FILE
// moves the clock Date.now reads by the milliseconds the file holds; HOOK_FAST_CODE runs the first ten-minute timer after
// 100 ms; HOOK_REQUEST_LOG appends each request the bridge receives, its request line and headers, as one JSON line;
// HOOK_DROP_FILE, once it exists, destroys every connection to the bridge and removes it.
const hookScript = `import http from "node:http";
import { syncBuiltinESMExports } from "node:module";
import { appendFileSync, existsSync, readFileSync, unlinkSync } from "node:fs";
if (process.env.HOOK_SKEW_FILE) {
  const now = Date.now.bind(Date);
  Date.now = () => { let skew = 0; try { skew = Number(readFileSync(process.env.HOOK_SKEW_FILE, "utf8")) || 0; } catch {} return now() + skew; };
}
if (process.env.HOOK_FAST_CODE) {
  const set = globalThis.setTimeout;
  let fast = true;
  globalThis.setTimeout = (fn, ms, ...args) => { if (ms === 600000 && fast) { fast = false; ms = 100; } return set(fn, ms, ...args); };
}
const sockets = new Set();
const create = http.createServer;
http.createServer = (...args) => {
  const server = create(...args);
  if (process.env.HOOK_REQUEST_LOG) server.on("request", (req) => appendFileSync(process.env.HOOK_REQUEST_LOG, JSON.stringify({ line: req.method + " " + req.url + " HTTP/" + req.httpVersion, headers: req.headers }) + "\n"));
  server.on("connection", (socket) => { sockets.add(socket); socket.on("close", () => sockets.delete(socket)); });
  return server;
};
syncBuiltinESMExports();
if (process.env.HOOK_DROP_FILE) setInterval(() => { if (existsSync(process.env.HOOK_DROP_FILE)) { unlinkSync(process.env.HOOK_DROP_FILE); for (const socket of sockets) socket.destroy(); } }, 25).unref();
`

// demoArgs are the arguments of a bridge over a pig with the demo extension and the faux model.
func demoArgs(t *testing.T, more ...string) []string {
	t.Helper()
	return append([]string{"--pig", buildPig(t), "--", "--model", "test-faux/echo", "--no-session", "-e", "demo-extension.mjs"}, more...)
}

// startHooked runs the bridge with hookScript loaded; env switches its parts on. Its pig, if any, is the test's own home.
func startHooked(t *testing.T, env []string, args ...string) (bridge, *rawBridge) {
	t.Helper()
	nodeBinary(t)
	hook := filepath.Join(t.TempDir(), "hook.mjs")
	if err := os.WriteFile(hook, []byte(hookScript), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env = append([]string{"PIG_TEST_FAUX=1", "PIG_HOME=" + filepath.Join(home, "home"), "PI_HOME=" + filepath.Join(home, "pihome"),
		"PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR=" + filepath.Join(home, "piagent")}, env...)
	r := startRaw(t, []string{"--import", nodeurl.PathToFileURL(hook, runtime.GOOS == "windows")}, env, args)
	// The pigs the bridge started end with it.
	t.Cleanup(func() {
		if !r.ended {
			_ = r.end(t, endSignal())
		}
	})
	return bridgeFrom(t, r.firstLine(t)), r
}

// loggedRequest is a request the bridge received, from HOOK_REQUEST_LOG.
type loggedRequest struct {
	Line    string
	Headers map[string]any
}

func (l loggedRequest) header(name string) string {
	if value, ok := l.Headers[name]; ok {
		return fmt.Sprint(value)
	}
	return ""
}

func (l loggedRequest) String() string {
	var parts []string
	for name, value := range l.Headers {
		parts = append(parts, name+": "+fmt.Sprint(value))
	}
	return l.Line + "\n" + strings.Join(parts, "\n")
}

func readRequestLog(t *testing.T, path string) []loggedRequest {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var all []loggedRequest
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<22)
	for scanner.Scan() {
		var entry loggedRequest
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		all = append(all, entry)
	}
	return all
}

func countRequests(log []loggedRequest, prefix string) int {
	n := 0
	for _, entry := range log {
		if strings.HasPrefix(entry.Line, prefix) {
			n++
		}
	}
	return n
}

// pageSecret is the one thing the page keeps in sessionStorage: the session secret.
func pageSecret(t *testing.T, browser *browsertest.Browser) string {
	t.Helper()
	var values []string
	if err := json.Unmarshal([]byte(browser.String(`JSON.stringify(Object.values(sessionStorage))`)), &values); err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || !secretForm.MatchString(values[0]) {
		t.Fatalf("sessionStorage holds %q, want the session secret and nothing else", values)
	}
	return values[0]
}

func pageSays(browser *browsertest.Browser, message string) {
	browser.Wait(`document.getElementById("state").textContent === ` + strconv.Quote(message))
}

// The code works once. A request that fails the Host or Origin rule, one with a wrong or a malformed code and one that
// puts the code in the address use nothing: the code is still good after them. The right code gives a secret, and the
// same code a second time is refused and gives nothing. The secret works in the Authorization header and nowhere else.
func TestBridgeCodeIsOneTimeAndAReplayIsRefused(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	b := bridgeFrom(t, r.firstLine(t))
	code := b.code(t)
	wrong := code[:31] + map[bool]string{true: "1", false: "0"}[code[31] == '0']
	for _, c := range []struct {
		what, path, body, host string
		header                 []string
		want                   int
	}{
		{"a foreign Origin", "/exchange", codeBody(code), "", []string{"Origin", "http://example.com"}, 403},
		{"the Origin of another port", "/exchange", codeBody(code), "", []string{"Origin", "http://127.0.0.1:1"}, 403},
		{"a foreign Host", "/exchange", codeBody(code), "evil.example", nil, 403},
		{"a wrong code", "/exchange", codeBody(wrong), "", nil, 403},
		{"the code in upper case", "/exchange", codeBody(strings.ToUpper(code) + "x"), "", nil, 403},
		{"a short code", "/exchange", codeBody(code[:31]), "", nil, 403},
		{"no code", "/exchange", `{}`, "", nil, 400},
		{"a code that is not a string", "/exchange", `{"code":123}`, "", nil, 400},
		{"a body that is not JSON", "/exchange", code, "", nil, 400},
		{"an empty body", "/exchange", "", "", nil, 400},
		{"the code in the address", "/exchange?code=" + code, "", "", nil, 400},
		{"the code as token in the address", "/exchange?token=" + code, `{}`, "", nil, 400},
		{"a body of more than 4 KiB", "/exchange", strings.Repeat("x", 5000), "", nil, 413},
	} {
		status, body, header, err := b.post(c.path, c.body, c.host, c.header...)
		if err != nil {
			t.Fatal(err)
		}
		if status != c.want || strings.Contains(body, "secret") || header.Get("Set-Cookie") != "" {
			t.Errorf("%s: POST /exchange = %d %q, want %d and no secret", c.what, status, body, c.want)
		}
		if got := b.code(t); got != code {
			t.Fatalf("%s used the code: the file holds another one now", c.what)
		}
	}
	if status := statusOf(t, b.base+"/exchange", ""); status != 403 {
		t.Errorf("GET /exchange = %d, want 403", status)
	}

	status, body, header, err := b.post("/exchange", codeBody(code), "")
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		t.Fatalf("POST /exchange with the live code = %d %q, want 200", status, body)
	}
	secret := secretIn(t, body)
	if header.Get("Set-Cookie") != "" || !strings.Contains(header.Get("Cache-Control"), "no-store") {
		t.Errorf("the exchange sets the cookie %q and Cache-Control %q, want no cookie and no-store", header.Get("Set-Cookie"), header.Get("Cache-Control"))
	}

	// The replay: refused, and nothing in the answer.
	status, body, header, err = b.post("/exchange", codeBody(code), "")
	if err != nil {
		t.Fatal(err)
	}
	if status != 403 || strings.Contains(body, "secret") || header.Get("Set-Cookie") != "" {
		t.Errorf("the used code again: POST /exchange = %d %q, want 403 and no secret", status, body)
	}
	holdsNoSecret(t, "the answer to the replay", body, secret)
	// The code authorizes nothing but its one exchange.
	if got := statusOf(t, b.base+"/state", "", "Authorization", "Bearer "+code); got != 403 {
		t.Errorf("GET /state with the used code as the secret = %d, want 403", got)
	}
	// The secret works as a header on the page's own host and origin, and in no address.
	auth := "Bearer " + secret
	if got := statusOf(t, b.base+"/state", "", "Authorization", auth); got != 200 {
		t.Errorf("GET /state with the secret = %d, want 200", got)
	}
	for _, path := range []string{"/state?token=" + secret, "/state?secret=" + secret, "/state?authorization=" + secret, "/events?token=" + secret} {
		if got := statusOf(t, b.base+path, ""); got != 403 {
			t.Errorf("GET %s = %d, want 403: the secret is taken from the header only", path, got)
		}
	}
	if got := statusOf(t, b.base+"/state", "", "Authorization", auth, "Origin", "http://example.com"); got != 403 {
		t.Errorf("GET /state with the secret and a foreign Origin = %d, want 403", got)
	}
	if got := statusOf(t, b.base+"/state", "evil.example", "Authorization", auth); got != 403 {
		t.Errorf("GET /state with the secret and a foreign Host = %d, want 403", got)
	}
	if got := statusOf(t, b.base+"/state", "", "Authorization", auth, "Origin", b.base); got != 200 {
		t.Errorf("GET /state with the secret and the bridge's own Origin = %d, want 200", got)
	}
	// A POST from a page of another origin carries the secret and is refused too.
	if status, _, _, _ := b.post("/send?session=x", "{}", "", "Authorization", auth, "Origin", "http://example.com"); status != 403 {
		t.Errorf("POST /send with the secret and a foreign Origin = %d, want 403", status)
	}
	// localhost is the bridge's name as well: the recorder of the release film opens the address that way.
	_, port, _ := strings.Cut(strings.TrimPrefix(b.base, "http://"), ":")
	next := b.code(t)
	if status, body, _, _ := b.post("/exchange", codeBody(next), "localhost:"+port, "Origin", "http://localhost:"+port); status != 200 {
		t.Errorf("POST /exchange for localhost:%s = %d %s, want 200", port, status, body)
	}
}

// Two requests that race for one code: exactly one is given a secret, and the next code is made once.
func TestBridgeCodeGivesOneSecretToTwoRacingRequests(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	b := bridgeFrom(t, r.firstLine(t))
	code := b.code(t)
	const racers = 32
	var won, refused atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range racers {
		wg.Go(func() {
			<-start
			status, _, _, err := b.post("/exchange", codeBody(code), "")
			switch {
			case err != nil:
				t.Errorf("POST /exchange: %v", err)
			case status == 200:
				won.Add(1)
			case status == 403:
				refused.Add(1)
			default:
				t.Errorf("POST /exchange = %d", status)
			}
		})
	}
	close(start)
	wg.Wait()
	if won.Load() != 1 || refused.Load() != racers-1 {
		t.Errorf("%d requests were given a secret and %d were refused, want 1 and %d", won.Load(), refused.Load(), racers-1)
	}
	if next := b.code(t); next == code {
		t.Error("the file holds the used code")
	} else if status, _ := b.exchange(t, next); status != 200 {
		t.Errorf("the code made after the race = %d, want 200", status)
	}
}

// A code lasts ten minutes (Date.now is moved here), and one that was used up when it expired is refused whatever the
// clock says later: it is used up either way, as the owner link of chord-todo is. The refusal gives nothing and the
// file holds the next code.
func TestBridgeExpiredCodeIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		skew time.Duration
		want int
	}{{"nine minutes and fifty-nine seconds", 599 * time.Second, 200}, {"ten minutes and one second", 601 * time.Second, 403}} {
		t.Run(c.name, func(t *testing.T) {
			skew := filepath.Join(t.TempDir(), "skew")
			b, _ := startHooked(t, []string{"HOOK_SKEW_FILE=" + skew})
			code := b.code(t)
			if err := os.WriteFile(skew, []byte(strconv.Itoa(int(c.skew.Milliseconds()))), 0o600); err != nil {
				t.Fatal(err)
			}
			status, body, header, err := b.post("/exchange", codeBody(code), "")
			if err != nil {
				t.Fatal(err)
			}
			if status != c.want {
				t.Fatalf("POST /exchange after %v = %d %q, want %d", c.skew, status, body, c.want)
			}
			if c.want == 403 && (strings.Contains(body, "secret") || header.Get("Set-Cookie") != "") {
				t.Errorf("the refusal of an expired code answers %q", body)
			}
			if err := os.WriteFile(skew, []byte("0"), 0o600); err != nil {
				t.Fatal(err)
			}
			if status, _ := b.exchange(t, code); status != 403 {
				t.Errorf("the same code with the clock back = %d, want 403: it was used up", status)
			}
			if next := b.code(t); next == code {
				t.Error("the file still holds the code that was used")
			} else if status, _ := b.exchange(t, next); status != 200 {
				t.Errorf("the code in the file after it = %d, want 200", status)
			}
		})
	}
}

// A code nobody used is replaced when its ten minutes are over (the timer is 100 ms here): the old one is refused and
// the file holds a new one that works. That the timer does not keep the process alive is TestBridgeReplacesACodeNobodyUsedHoldsNoTimer.
func TestBridgeReplacesACodeNobodyUsed(t *testing.T) {
	b, _ := startHooked(t, []string{"HOOK_FAST_CODE=1"})
	old := b.code(t)
	deadline := time.Now().Add(10 * time.Second)
	for b.code(t) == old {
		if time.Now().After(deadline) {
			t.Fatal("the code was not replaced in 10 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status, _ := b.exchange(t, old); status != 403 {
		t.Errorf("the replaced code = %d, want 403", status)
	}
	if status, _ := b.exchange(t, b.code(t)); status != 200 {
		t.Errorf("the code that replaced it = %d, want 200", status)
	}
}

// A use of the code makes the next one, written to address and open.html before the answer, by rename: the files never
// hold a used code, a reader never sees half a file, the directory holds those two files and nothing else, and both stay
// mode 0600. After the exit the directory is gone.
func TestBridgeWritesTheNextCodeBeforeItAnswers(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	b := bridgeFrom(t, r.firstLine(t))
	dir := filepath.Dir(b.addressFile())
	var mu sync.Mutex
	used := map[string]bool{}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var once sync.Once
	shutdown := func() {
		once.Do(func() { close(stop) })
		readers.Wait()
	}
	t.Cleanup(shutdown)
	readers.Go(func() {
		whole := regexp.MustCompile(`#([0-9a-f]{32})`)
		for {
			select {
			case <-stop:
				return
			default:
			}
			mu.Lock()
			before := map[string]bool{}
			for code := range used {
				before[code] = true
			}
			mu.Unlock()
			for _, name := range []string{"address", "open.html"} {
				content, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Errorf("reading %s while the codes are used: %v", name, err)
					return
				}
				m := whole.FindStringSubmatch(string(content))
				if m == nil {
					t.Errorf("%s holds no whole address while the codes are used: %q", name, content)
					return
				}
				if before[m[1]] {
					t.Errorf("%s holds the code %s after it was used", name, m[1])
					return
				}
			}
		}
	})
	seen := map[string]bool{}
	for range 25 {
		code := b.code(t)
		if seen[code] {
			t.Fatalf("the code %s was made twice", code)
		}
		seen[code] = true
		if status, body := b.exchange(t, code); status != 200 {
			t.Fatalf("POST /exchange = %d %s", status, body)
		}
		mu.Lock()
		used[code] = true
		mu.Unlock()
		// The files hold the next code by the time the answer is here, and open.html names it too.
		if next := b.code(t); next == code {
			t.Fatal("the address file holds the used code after the answer")
		} else if page, _ := os.ReadFile(b.openFile); !strings.Contains(string(page), b.base+"/#"+next) || strings.Contains(string(page), code) {
			t.Fatalf("open.html after the answer:\n%s\nwant the address with %s and not %s", page, next, code)
		}
	}
	shutdown()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
		if runtime.GOOS != "windows" {
			if info, err := entry.Info(); err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("%s is %v (%v), want mode 0600", entry.Name(), info, err)
			}
		}
	}
	if strings.Join(names, " ") != "address open.html" {
		t.Errorf("the private directory holds %v after the codes were used, want address and open.html", names)
	}
	_ = r.end(t, endSignal())
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the directory is still there after the exit (%v)", err)
	}
}

// The bridge keeps the newest 64 secrets: one more drops the oldest, which is refused from then on, and the others work.
func TestBridgeKeepsAtMostSixtyFourSecrets(t *testing.T) {
	r := startRaw(t, nil, nil, nil)
	b := bridgeFrom(t, r.firstLine(t))
	var secrets []string
	for range maxSecrets + 1 {
		status, body := b.exchange(t, b.code(t))
		if status != 200 {
			t.Fatalf("POST /exchange = %d %s", status, body)
		}
		secrets = append(secrets, secretIn(t, body))
	}
	for i, secret := range secrets {
		want := 200
		if i == 0 {
			want = 403
		}
		if got := statusOf(t, b.base+"/state", "", "Authorization", "Bearer "+secret); got != want {
			t.Errorf("GET /state with secret %d of %d = %d, want %d", i+1, len(secrets), got, want)
		}
	}
}

// Every request URL the page makes, as Chromium sends it and as the bridge receives it, holds neither the code nor the
// secret in any form; no Referer and no cookie carries them; the address bar is `/` after the load and after a reload;
// the navigation history holds no code; the bridge sees the code once, in a body. The reload keeps the session: it
// exchanges nothing.
func TestPageSendsNoCodeOrSecretInAnyRequest(t *testing.T) {
	log := filepath.Join(t.TempDir(), "requests.jsonl")
	b, _ := startHooked(t, []string{"HOOK_REQUEST_LOG=" + log}, demoArgs(t)...)
	browser := browsertest.Start(t)
	var mu sync.Mutex
	type seen struct{ url, fragment, referer string }
	var requests []seen
	browser.On("Network.requestWillBeSent", func(params json.RawMessage) {
		var event struct {
			Request struct {
				URL         string            `json:"url"`
				URLFragment string            `json:"urlFragment"`
				Headers     map[string]string `json:"headers"`
			} `json:"request"`
		}
		_ = json.Unmarshal(params, &event)
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, seen{event.Request.URL, event.Request.URLFragment, event.Request.Headers["Referer"]})
	})
	if _, err := browser.Call(t.Context(), "Network.enable", nil); err != nil {
		t.Fatal(err)
	}
	code := b.code(t)
	b.openFileIn(t, browser)
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	secret := pageSecret(t, browser)
	if got := browser.String(`location.href`); got != b.base+"/" {
		t.Errorf("the address is %q after the load, want %q", got, b.base+"/")
	}
	// A line from the page: its address holds the session and no secret.
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.focus(); form.elements.message.value = "/ui-demo"; form.requestSubmit(); })()`)
	browser.Wait(`document.querySelector('[data-key="demo"]') !== null`)
	browser.Run(`window.beforeReload = true; location.reload()`)
	browser.Wait(`!window.beforeReload && window.uiView && window.uiView.attachment === 1`)
	if got := browser.String(`location.href`); got != b.base+"/" {
		t.Errorf("the address is %q after a reload, want %q", got, b.base+"/")
	}
	if got := pageSecret(t, browser); got != secret {
		t.Errorf("the reload changed the secret in sessionStorage")
	}

	mu.Lock()
	defer mu.Unlock()
	var events, sends int
	fragments := 0
	for _, r := range requests {
		for _, held := range []string{code, secret} {
			holdsNoSecret(t, "the request URL "+r.url, r.url, held)
			holdsNoSecret(t, "the Referer of "+r.url, r.referer, held)
		}
		if strings.HasPrefix(r.url, b.base+"/events") {
			events++
		}
		if strings.HasPrefix(r.url, b.base+"/send") {
			sends++
		}
		if r.fragment != "" {
			// Only the address as the tab opened it carries the code, in its fragment: it is never sent.
			fragments++
			if r.url != b.base+"/" || r.fragment != "#"+code {
				t.Errorf("the request %s carries the fragment %s", r.url, r.fragment)
			}
		}
	}
	if events < 2 || sends == 0 {
		t.Errorf("Chromium sent %d requests for the stream and %d for lines (of %d), want the stream before and after the reload and the line", events, sends, len(requests))
	}
	if fragments != 1 {
		t.Errorf("%d requests carry a fragment, want the one of the first load", fragments)
	}
	history, err := browser.Call(t.Context(), "Page.getNavigationHistory", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The navigation entry's url is the address bar's; the browser also keeps the address as it was typed, with the code in
	// its fragment: that code is used up by now (TestBridgeCodeIsOneTimeAndAReplayIsRefused), and the secret is in neither.
	var entries struct {
		Entries []struct{ URL, UserTypedURL string }
	}
	if err := json.Unmarshal(history, &entries); err != nil || len(entries.Entries) == 0 {
		t.Fatalf("the navigation history %s: %v", history, err)
	}
	for _, entry := range entries.Entries {
		holdsNoSecret(t, "the navigation entry's address", entry.URL, code)
		holdsNoSecret(t, "the navigation entry's address", entry.URL, secret)
		holdsNoSecret(t, "the navigation entry's typed address", entry.UserTypedURL, secret)
	}
	// After the bridge's log was read (this is one more request): the code the browser kept is dead.
	defer func() {
		if status, _ := b.exchange(t, code); status != http.StatusForbidden {
			t.Errorf("the code the browser kept in its history = %d, want 403: it is used up", status)
		}
	}()
	if got := browser.String(`document.cookie`); got != "" {
		t.Errorf("document.cookie = %q, want none", got)
	}
	cookies, err := browser.Call(t.Context(), "Network.getAllCookies", nil)
	if err != nil {
		t.Fatal(err)
	}
	var jar struct{ Cookies []json.RawMessage }
	_ = json.Unmarshal(cookies, &jar)
	if len(jar.Cookies) != 0 {
		t.Errorf("the browser holds the cookies %s after the page, want none", cookies)
	}

	// The same from the bridge's side: each request line it received and each header.
	got := readRequestLog(t, log)
	for _, entry := range got {
		for _, held := range []string{code, secret} {
			text := entry.String()
			if auth := entry.header("authorization"); auth != "" {
				text = strings.ReplaceAll(text, auth, "")
			}
			holdsNoSecret(t, "the request the bridge received: "+entry.Line, text, held)
		}
		if cookie := entry.Headers["cookie"]; cookie != nil {
			t.Errorf("%s carries the cookie %v", entry.Line, cookie)
		}
		wantAuth := ""
		if strings.HasPrefix(entry.Line, "GET /events ") || strings.HasPrefix(entry.Line, "POST /send?session=") {
			wantAuth = "Bearer " + secret
		}
		if auth, _ := entry.Headers["authorization"].(string); auth != wantAuth {
			t.Errorf("%s carries the Authorization %q, want %q", entry.Line, auth, wantAuth)
		}
	}
	if n := countRequests(got, "POST /exchange HTTP/1.1"); n != 1 {
		t.Errorf("the bridge received %d exchanges, want 1: the reload keeps the session", n)
	}
	if n := countRequests(got, "GET /events HTTP/1.1"); n != events {
		t.Errorf("the bridge received %d stream requests, Chromium sent %d", n, events)
	}
}

// The address, used once, is refused to a second browser (another profile, as another user's is): the page says so, holds
// nothing in sessionStorage and does not ask for the stream.
func TestPageSaysAReplayedAddressIsUsed(t *testing.T) {
	log := filepath.Join(t.TempDir(), "requests.jsonl")
	b, _ := startHooked(t, []string{"HOOK_REQUEST_LOG=" + log}, demoArgs(t)...)
	first := browsertest.Start(t)
	old := b.code(t)
	first.Open(b.base + "/#" + old)
	first.Wait(`window.uiView && window.uiView.attachment === 1`)
	second := browsertest.Start(t)
	second.Open(b.base + "/#" + old)
	pageSays(second, msgUsed)
	if got := second.String(`String(sessionStorage.length) + "|" + location.href`); got != "0|"+b.base+"/" {
		t.Errorf("the second page holds %q, want nothing in sessionStorage and the address %s/", got, b.base)
	}
	time.Sleep(500 * time.Millisecond)
	if n := countRequests(readRequestLog(t, log), "GET /events"); n != 1 {
		t.Errorf("the bridge received %d stream requests, want the first page's one", n)
	}
	// The page that was signed in is not touched by the replay.
	if got := first.String(`String(window.uiView.attachment)`); got != "1" {
		t.Errorf("the first page's attachment = %s after the replay, want 1", got)
	}
}

// A tab that was opened at the bridge without an address and has no secret says so and asks for nothing.
func TestPageWithoutASessionSaysSo(t *testing.T) {
	log := filepath.Join(t.TempDir(), "requests.jsonl")
	b, _ := startHooked(t, []string{"HOOK_REQUEST_LOG=" + log}, demoArgs(t)...)
	browser := browsertest.Start(t)
	browser.Open(b.base + "/")
	pageSays(browser, msgSignedOut)
	time.Sleep(500 * time.Millisecond)
	got := readRequestLog(t, log)
	if n := countRequests(got, "GET /events") + countRequests(got, "POST /exchange") + countRequests(got, "POST /send"); n != 0 {
		t.Errorf("the bridge received %d requests of a page that holds no secret, want none", n)
	}
}

// The stream is a fetch with the Authorization header, and when the connection breaks the page connects again with the
// same secret: a new session, no second exchange, and the page draws again.
func TestPageStreamReconnectsWithItsSecret(t *testing.T) {
	dir := t.TempDir()
	log, drop := filepath.Join(dir, "requests.jsonl"), filepath.Join(dir, "drop")
	b, _ := startHooked(t, []string{"HOOK_REQUEST_LOG=" + log, "HOOK_DROP_FILE=" + drop}, demoArgs(t)...)
	browser := browsertest.Start(t)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	secret := pageSecret(t, browser)
	first := browser.String(`window.uiView.session`)
	if err := os.WriteFile(drop, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	browser.Wait(`window.uiView.session !== "" && window.uiView.session !== ` + strconv.Quote(first) + ` && window.uiView.attachment === 1`)
	if got := pageSecret(t, browser); got != secret {
		t.Error("the secret changed with the reconnection")
	}
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.focus(); form.elements.message.value = "/ui-demo"; form.requestSubmit(); })()`)
	browser.Wait(`document.querySelector('[data-key="demo"]') !== null`)
	got := readRequestLog(t, log)
	if n := countRequests(got, "POST /exchange"); n != 1 {
		t.Errorf("the bridge received %d exchanges, want 1", n)
	}
	streams := 0
	for _, entry := range got {
		if entry.Line == "GET /events HTTP/1.1" {
			streams++
			if auth, _ := entry.Headers["authorization"].(string); auth != "Bearer "+secret {
				t.Errorf("a stream request carries the Authorization %q, want the page's secret", auth)
			}
		}
	}
	if streams != 2 {
		t.Errorf("the bridge received %d stream requests, want the first and one reconnection", streams)
	}
}

// A secret the bridge does not know (here one that 64 newer secrets pushed out; a restarted bridge knows none): a refusal of
// the stream shows the message, drops the secret from sessionStorage and the page does not ask again; so does a refusal of a
// line the user sends.
func TestPageDropsASecretTheBridgeRefuses(t *testing.T) {
	forget := func(t *testing.T, b bridge) {
		t.Helper()
		for range maxSecrets {
			if status, body := b.exchange(t, b.code(t)); status != 200 {
				t.Fatalf("POST /exchange = %d %s", status, body)
			}
		}
	}
	t.Run("the stream", func(t *testing.T) {
		dir := t.TempDir()
		log, drop := filepath.Join(dir, "requests.jsonl"), filepath.Join(dir, "drop")
		b, _ := startHooked(t, []string{"HOOK_REQUEST_LOG=" + log, "HOOK_DROP_FILE=" + drop}, demoArgs(t)...)
		browser := browsertest.Start(t)
		b.openIn(t, browser, "")
		browser.Wait(`window.uiView && window.uiView.attachment === 1`)
		pageSecret(t, browser)
		forget(t, b)
		if err := os.WriteFile(drop, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		pageSays(browser, msgRestarted)
		if got := browser.String(`String(sessionStorage.length)`); got != "0" {
			t.Errorf("sessionStorage holds %s entries after the refusal, want none", got)
		}
		time.Sleep(2500 * time.Millisecond)
		if n := countRequests(readRequestLog(t, log), "GET /events"); n != 2 {
			t.Errorf("the bridge received %d stream requests, want the first and the one that was refused", n)
		}
		pageSays(browser, msgRestarted)
	})
	t.Run("a line", func(t *testing.T) {
		b := startBridge(t)
		browser := browsertest.Start(t)
		b.openIn(t, browser, "")
		browser.Wait(`window.uiView && window.uiView.attachment === 1`)
		forget(t, b)
		browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.value = "hello"; form.requestSubmit(); })()`)
		pageSays(browser, msgRestarted)
		if got := browser.String(`String(sessionStorage.length)`); got != "0" {
			t.Errorf("sessionStorage holds %s entries after the refusal, want none", got)
		}
	})
}
