// Package smoke launches the built desktop app and drives it from outside, the way a person's machine would: it is the macOS
// smoke test and the measurement of the desktop app against `p2 server` and a browser tab. It runs only when
// P2_DESKTOP_BIN names the app's executable (the inside of PiG.app); see docs/p2-desktop.md in apps/p2desktop.
package smoke

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

func binary(t *testing.T) string {
	bin := os.Getenv("P2_DESKTOP_BIN")
	if bin == "" {
		t.Skip("P2_DESKTOP_BIN is not set")
	}
	return bin
}

// proc is one row of ps.
type proc struct {
	pid, ppid int
	rssKB     int
	cpu       time.Duration
	comm      string
}

var cpuTime = regexp.MustCompile(`^(?:(\d+):)?(\d+):(\d+\.\d+)$`)

func parseCPU(s string) time.Duration {
	m := cpuTime.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	sec, _ := strconv.ParseFloat(m[3], 64)
	return time.Duration(h)*time.Hour + time.Duration(min)*time.Minute + time.Duration(sec*float64(time.Second))
}

func processes(t *testing.T) map[int]proc {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=,time=,comm=").Output()
	if err != nil {
		t.Fatal(err)
	}
	all := map[int]proc{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		rss, _ := strconv.Atoi(f[2])
		all[pid] = proc{pid, ppid, rss, parseCPU(f[3]), strings.Join(f[4:], " ")}
	}
	return all
}

// tree is root and its descendants.
func tree(all map[int]proc, root int) []proc {
	var out []proc
	var walk func(int)
	walk = func(pid int) {
		if p, ok := all[pid]; ok {
			out = append(out, p)
		}
		for _, p := range all {
			if p.ppid == pid && p.pid != pid {
				walk(p.pid)
			}
		}
	}
	walk(root)
	return out
}

// started is the processes that exist now and did not in before, whose command matches names: the WebKit helpers a window
// starts are children of launchd, not of the app.
func started(before, now map[int]proc, names *regexp.Regexp) []proc {
	var out []proc
	for pid, p := range now {
		if _, ok := before[pid]; !ok && names.MatchString(p.comm) {
			out = append(out, p)
		}
	}
	return out
}

type app struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	data   string
	state  string
	port   int
	t0     time.Time
}

func launch(t *testing.T, bin string, extraEnv ...string) *app {
	t.Helper()
	root := t.TempDir()
	a := &app{stderr: &bytes.Buffer{}, data: filepath.Join(root, "data"), state: filepath.Join(root, "state")}
	a.cmd = exec.Command(bin)
	a.cmd.Stderr = a.stderr
	a.cmd.Env = append(os.Environ(), "P2_FAKE=1", "P2_DATA_DIR="+a.data, "P2_DESKTOP_STATE_DIR="+a.state, "P2_DESKTOP_LEAKCHECK=1", "P2_IDLE_MS=3000")
	a.cmd.Env = append(a.cmd.Env, extraEnv...)
	a.t0 = time.Now()
	if err := a.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a.cmd.ProcessState == nil {
			_ = a.cmd.Process.Kill()
			_ = a.cmd.Wait()
		}
	})
	return a
}

// waitHealth waits for the server's port (the app records it in its state file) and for /health; it returns the time from
// launch to the first answer.
func (a *app) waitHealth(t *testing.T) time.Duration {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(a.state, "state.json")); err == nil {
			var s struct{ Port int }
			if json.Unmarshal(b, &s) == nil && s.Port != 0 {
				a.port = s.Port
				resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(s.Port) + "/health")
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode == 200 {
						return time.Since(a.t0)
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no /health after 60 s; stderr:\n%s", a.stderr.String())
	return 0
}

func osascript(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		t.Logf("osascript: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// windows lists the app's windows from the window server (CGWindowList, which works while the screen is locked, where
// System Events and screencapture see nothing): P2_WINDOWS_BIN is windows.swift compiled with swiftc.
func (a *app) windows(t *testing.T) string {
	t.Helper()
	helper := os.Getenv("P2_WINDOWS_BIN")
	if helper == "" {
		t.Skip("P2_WINDOWS_BIN is not set (swiftc -O -o /tmp/cgw smoke/windows.swift)")
	}
	out, err := exec.Command(helper, strconv.Itoa(a.cmd.Process.Pid)).CombinedOutput()
	if err != nil {
		t.Logf("windows helper: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func (a *app) waitWindow(t *testing.T) time.Duration {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if out := a.windows(t); strings.Contains(out, "onscreen=1 name=PiG bounds=1280x840") {
			return time.Since(a.t0)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no window after 30 s: %s; stderr:\n%s", a.windows(t), a.stderr.String())
	return 0
}

var windowID = regexp.MustCompile(`id=(\d+) layer=0 onscreen=1 name=PiG `)

// screenshot saves the app's window to dir/name (P2_DESKTOP_SHOTS), through its window id: screencapture -l works while the
// screen is locked, where capturing the screen gives black.
func (a *app) screenshot(t *testing.T, name string) {
	dir := os.Getenv("P2_DESKTOP_SHOTS")
	if dir == "" {
		return
	}
	m := windowID.FindStringSubmatch(a.windows(t))
	if m == nil {
		t.Errorf("no window to capture")
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	if out, err := exec.Command("screencapture", "-x", "-l", m[1], filepath.Join(dir, name)).CombinedOutput(); err != nil {
		t.Errorf("screencapture: %v %s", err, out)
	}
}

// chat runs the faux-provider round trip over the app's own /chord socket, as the web UI does: create a session, send,
// wait for the assistant's answer.
func chat(t *testing.T, port int) string {
	t.Helper()
	conn, err := p2test.Dial(port, nil)
	if err != nil {
		t.Fatalf("dial /chord: %v", err)
	}
	browser, err := p2test.ClientHost(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browser.Close(p2test.Background) }()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", t.TempDir(), "title", "smoke"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", object(created).Value("id"), "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Release()
	if _, err := ref.Call("send", delta.JsonObjectOf("text", "hello from the desktop smoke", "requestId", "smoke-1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	var answer string
	if _, err := p2test.Until(ref.Member("view"), func(view delta.JsonValue) bool {
		entries, _ := object(view).Value("entries").([]any)
		for _, entry := range entries {
			model, _ := object(entry).Value("model").([]any)
			if len(model) > 0 && object(model[0]).Value("role") == "assistant" {
				var text strings.Builder
				content, _ := object(model[0]).Value("content").([]any)
				for _, part := range content {
					if object(part).Value("type") == "text" {
						text.WriteString(object(part).Value("text").(string))
					}
				}
				answer = text.String()
				return answer != ""
			}
		}
		return false
	}, 30*time.Second); err != nil {
		t.Fatalf("no answer: %v", err)
	}
	return answer
}

// TestSmoke is the macOS smoke: launch, a window on the server's page, a chat round trip, a clean quit.
func TestSmoke(t *testing.T) {
	bin := binary(t)
	before := processes(t)
	a := launch(t, bin)
	health := a.waitHealth(t)
	window := a.waitWindow(t)
	t.Logf("server answered /health %v after launch; the window existed after %v", health.Round(time.Millisecond), window.Round(time.Millisecond))
	time.Sleep(1500 * time.Millisecond) // the page loads
	t.Logf("window: %s", a.windows(t))
	a.screenshot(t, "p2-desktop-window.png")
	// The web view's networking process holds a connection to the server: the window loaded the page from it (the screen of
	// this Mac is locked when the smoke runs unattended, so there is no screenshot).
	lsof, _ := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(a.port)).Output()
	t.Logf("connections to the server port:\n%s", lsof)
	webkit := regexp.MustCompile(`WebKit`)
	for _, p := range started(before, processes(t), webkit) {
		t.Logf("web view process: %d %s", p.pid, filepath.Base(p.comm))
	}

	answer := chat(t, a.port)
	t.Logf("faux provider answered: %q", answer)
	if answer == "" {
		t.Fatal("empty answer")
	}

	// Quit as the system asks an app to: SIGTERM is MyGo's quit (a second signal would exit at once).
	pid := a.cmd.Process.Pid
	if err := a.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v\nstderr:\n%s", err, a.stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the app did not quit; stderr:\n%s", a.stderr.String())
	}
	t.Logf("stderr:\n%s", a.stderr.String())
	if !strings.Contains(a.stderr.String(), "leak check: no goroutine outlived the server") {
		t.Fatalf("no leak check result")
	}
	// No orphaned process: the session workers were children of the app.
	time.Sleep(500 * time.Millisecond)
	now := processes(t)
	for pid, p := range now {
		if _, ok := before[pid]; !ok && strings.Contains(p.comm, filepath.Base(bin)) {
			t.Errorf("orphaned process %d %s (parent %d)", pid, p.comm, p.ppid)
		}
	}
	if _, ok := now[pid]; ok {
		t.Errorf("the app process %d is still there", pid)
	}
	if resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(a.port) + "/health"); err == nil {
		_ = resp.Body.Close()
		t.Errorf("the port is still served")
	}
}

type sample struct {
	rssMB   float64
	cpuPct  float64
	nproc   int
	members []string
}

// idleSample sums the resident memory of the members, and their CPU as a percentage of one core, over a window of top's second
// sample (top computes it between its two samples; ps's TIME has a granularity of a second).
func idleSample(t *testing.T, members func(map[int]proc) []proc, settle, window time.Duration) sample {
	t.Helper()
	time.Sleep(settle)
	out, err := exec.Command("top", "-l", "2", "-s", strconv.Itoa(int(window.Seconds())), "-stats", "pid,cpu").Output()
	if err != nil {
		t.Fatalf("top: %v", err)
	}
	cpu := map[int]float64{}
	blocks := strings.Split(string(out), "PID")
	last := blocks[len(blocks)-1]
	for _, line := range strings.Split(last, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			pid, err1 := strconv.Atoi(f[0])
			pct, err2 := strconv.ParseFloat(f[1], 64)
			if err1 == nil && err2 == nil {
				cpu[pid] = pct
			}
		}
	}
	var s sample
	for _, p := range members(processes(t)) {
		s.rssMB += float64(p.rssKB) / 1024
		s.cpuPct += cpu[p.pid]
		s.members = append(s.members, fmt.Sprintf("%s(%.0f MB, %.1f%%)", filepath.Base(p.comm), float64(p.rssKB)/1024, cpu[p.pid]))
		s.nproc++
	}
	sort.Strings(s.members)
	return s
}

// TestMeasure measures the desktop app against `p2 server` with a browser tab open on it: time until the page is
// there, resident memory and CPU while idle. Both show p2's own page, so the difference is the shell's.
func TestMeasure(t *testing.T) {
	bin := binary(t)
	baseline := os.Getenv("P2_BASELINE_BIN")
	chrome := os.Getenv("P2_CHROME_BIN")
	if baseline == "" || chrome == "" {
		t.Skip("P2_BASELINE_BIN (a built apps/p2/cmd/p2) and P2_CHROME_BIN are not set")
	}
	const runs = 2
	webkit := regexp.MustCompile(`com\.apple\.WebKit`)
	for run := 1; run <= runs; run++ {
		// Desktop app: process tree (app, workers) plus the web view's helper processes that this launch started.
		before := processes(t)
		a := launch(t, bin, "P2_DESKTOP_LEAKCHECK=0")
		health := a.waitHealth(t)
		window := a.waitWindow(t)
		time.Sleep(2 * time.Second)
		s := idleSample(t, func(all map[int]proc) []proc {
			return append(tree(all, a.cmd.Process.Pid), started(before, all, webkit)...)
		}, 25*time.Second, 30*time.Second)
		t.Logf("RESULT desktop run=%d health=%v window=%v rss=%.0fMB cpu=%.2f%% procs=%d %v", run, health.Round(time.Millisecond), window.Round(time.Millisecond), s.rssMB, s.cpuPct, s.nproc, s.members)
		_ = a.cmd.Process.Signal(syscall.SIGTERM)
		_ = a.cmd.Wait()
		time.Sleep(2 * time.Second)

		// Baseline: the server alone plus a browser tab (a separate Chrome for Testing with its own profile).
		root := t.TempDir()
		server := exec.Command(baseline, "server")
		server.Env = append(os.Environ(), "P2_FAKE=1", "PORT=0", "P2_DATA_DIR="+filepath.Join(root, "data"))
		stdout, _ := server.StdoutPipe()
		t0 := time.Now()
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		line := make([]byte, 128)
		n, _ := stdout.Read(line)
		port := strings.TrimSuffix(strings.SplitN(strings.TrimPrefix(string(line[:n]), "p2: http://127.0.0.1:"), " ", 2)[0], "\n")
		for {
			resp, err := http.Get("http://127.0.0.1:" + port + "/health")
			if err == nil {
				_ = resp.Body.Close()
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		serverUp := time.Since(t0)
		browser := exec.Command(chrome, "--user-data-dir="+filepath.Join(root, "chrome"), "--no-first-run", "--no-default-browser-check", "--new-window", "http://127.0.0.1:"+port+"/")
		if err := browser.Start(); err != nil {
			t.Fatal(err)
		}
		// The tab is there when the browser's network process asks the server (the server's /health was ours).
		tab := time.Duration(0)
		for tab == 0 && time.Since(t0) < 30*time.Second {
			out, _ := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:ESTABLISHED").Output()
			if strings.Contains(string(out), "Google") {
				tab = time.Since(t0)
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(2 * time.Second)
		s = idleSample(t, func(all map[int]proc) []proc {
			return append(tree(all, server.Process.Pid), tree(all, browser.Process.Pid)...)
		}, 25*time.Second, 30*time.Second)
		serverOnly := tree(processes(t), server.Process.Pid)
		var serverMB float64
		for _, p := range serverOnly {
			serverMB += float64(p.rssKB) / 1024
		}
		t.Logf("RESULT server+tab run=%d health=%v tab=%v rss=%.0fMB (server %.0fMB) cpu=%.2f%% procs=%d", run, serverUp.Round(time.Millisecond), tab.Round(time.Millisecond), s.rssMB, serverMB, s.cpuPct, s.nproc)
		t.Logf("RESULT server+tab members run=%d %v", run, s.members)
		_ = browser.Process.Signal(syscall.SIGTERM)
		_ = browser.Wait()
		_ = server.Process.Signal(syscall.SIGTERM)
		_ = server.Wait()
		time.Sleep(2 * time.Second)
	}
}
