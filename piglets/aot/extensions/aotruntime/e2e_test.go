//go:build unix

package aotruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The end-to-end tests run a real pig binary in RPC mode against the real compiler. They skip unless the environment names what they need:
//
//	PIG_AOT_E2E_PIG          the pig binary
//	PIG_AOT_E2E_BUN          stock Bun 1.4.2 (its digest must be in PiG's runtime lock)
//	PIG_AOT_E2E_RELEASE_DIR  a directory with the Pi-Bolt 0.7.4 release files (SHA256SUMS, SHA256SUMS.sig, the runtime archive); the compiler
//	                         is then "downloaded" from it, verified against the pins in this Piglet. Without it the test downloads from GitHub.
type e2e struct {
	t      *testing.T
	pig    string
	slot   string
	home   string
	agent  string
	extDir string
	worker *worker
	env    []string
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	pig, bun := os.Getenv("PIG_AOT_E2E_PIG"), os.Getenv("PIG_AOT_E2E_BUN")
	if pig == "" || bun == "" {
		t.Skip("set PIG_AOT_E2E_PIG and PIG_AOT_E2E_BUN to run the end-to-end tests")
	}
	if platformName() != "linux-x64" {
		t.Skip("the end-to-end tests run on linux-x64")
	}
	root := t.TempDir()
	e := &e2e{t: t, pig: pig, slot: filepath.Join(root, "slot"), home: filepath.Join(root, "home"), extDir: filepath.Join(root, "ext")}
	e.agent = filepath.Join(e.home, ".pig", "agent")
	for _, dir := range []string{e.slot, e.agent, e.extDir, filepath.Join(e.slot, "bun"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(bun)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.slot, "bun", "bun"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	digest, _ := fileSHA256(filepath.Join(e.slot, "bun", "bun"))
	manifest := fmt.Sprintf(`{"id":"bun","kind":"bun","version":"1.4.2","platform":"linux-x64","provider":"test","image":{"executable":"bun","sha256":%q}}`, digest)
	if err := os.WriteFile(filepath.Join(e.slot, "bun", "runtime.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	e.env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + e.home, "TMPDIR=" + filepath.Join(root, "tmp"),
		"PIG_HOME=" + filepath.Join(e.home, ".pig"), "PI_HOME=" + filepath.Join(e.home, ".pi"),
		"PIG_CODING_AGENT_DIR=" + e.agent, "PI_CODING_AGENT_DIR=" + filepath.Join(e.home, ".pi", "agent"),
		"PIG_TS_RUNTIMES_DIR=" + e.slot, "PIG_TS_RUNTIME=aot", "PIG_OFFLINE=1", "PI_OFFLINE=1", "PIG_TELEMETRY=0", "PIG_SKIP_VERSION_CHECK=1",
	}
	e.worker = newWorker(e.slot, "linux-x64", nil)
	if dir := os.Getenv("PIG_AOT_E2E_RELEASE_DIR"); dir != "" {
		server := httptest.NewServer(http.FileServer(http.Dir(dir)))
		t.Cleanup(server.Close)
		e.worker.installer.baseURL = server.URL
	}
	// The worker's own downloads honor PIG_OFFLINE; the test runs pig offline and the worker online.
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	return e
}

// writeExtension writes the probe extension. version appears in /probe's answer, so a test can tell which source ran. nodeOnly makes the factory use an API that
// Bun and the compiled host lack and Node has.
func (e *e2e) writeExtension(version string, nodeOnly bool) string {
	e.t.Helper()
	source := `import v8 from "node:v8";
const VERSION = ` + fmt.Sprintf("%q", version) + `;
export default function (pi: any) {
` + map[bool]string{true: `  new v8.Serializer(); // Node has it; Bun's stub throws "not yet implemented"
`, false: ""}[nodeOnly] + `  pi.registerCommand("probe", { description: "probe", handler: async (_args: string, ctx: any) => {
    ctx.ui.notify("PROBE:" + JSON.stringify({ version: VERSION, pid: process.pid, execPath: process.execPath, bun: typeof (globalThis as any).Bun !== "undefined", nodeVersion: process.version, release: process.release?.name, argv0: process.argv[0], title: process.title }), "info");
  } });
  pi.registerCommand("doreload", { description: "reload", handler: async (_args: string, ctx: any) => { await ctx.reload(); } });
}
`
	entry := filepath.Join(e.extDir, "index.ts")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return entry
}

type rpcSession struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan map[string]any
	n     int
	err   *strings.Builder
}

func (e *e2e) start(entry string) *rpcSession {
	e.t.Helper()
	cmd := exec.Command(e.pig, "--mode", "rpc", "--no-session", "--offline", "-ne", "-e", entry)
	cmd.Env = e.env
	cmd.Dir = e.extDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	s := &rpcSession{t: e.t, cmd: cmd, stdin: stdin, lines: make(chan map[string]any, 256), err: stderr}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1<<20), 16<<20)
		for scanner.Scan() {
			var record map[string]any
			if json.Unmarshal(scanner.Bytes(), &record) == nil {
				s.lines <- record
			}
		}
		close(s.lines)
	}()
	e.t.Cleanup(func() { s.close() })
	return s
}

func (s *rpcSession) close() {
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}
}

func (s *rpcSession) send(record map[string]any) string {
	s.n++
	id := fmt.Sprintf("r%d", s.n)
	record["id"] = id
	data, _ := json.Marshal(record)
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// until reads events until pred holds.
func (s *rpcSession) until(pred func(map[string]any) bool) map[string]any {
	s.t.Helper()
	timeout := time.After(120 * time.Second)
	for {
		select {
		case record, ok := <-s.lines:
			if !ok {
				s.t.Fatalf("pig exited: %s", s.err.String())
			}
			if os.Getenv("PIG_AOT_E2E_DEBUG") != "" {
				s.t.Logf("rpc: %v", record)
			}
			if pred(record) {
				return record
			}
		case <-timeout:
			s.t.Fatalf("timeout waiting for an event; stderr: %s", s.err.String())
		}
	}
}

type probeAnswer struct {
	Version  string `json:"version"`
	Pid      int    `json:"pid"`
	ExecPath string `json:"execPath"`
	Bun      bool   `json:"bun"`
	NodeVer  string `json:"nodeVersion"`
	Release  string `json:"release"`
	Argv0    string `json:"argv0"`
}

// ready waits for the extension's commands, as the first RPC answer.
func (s *rpcSession) ready() {
	id := s.send(map[string]any{"type": "get_commands"})
	s.until(func(r map[string]any) bool { return r["type"] == "response" && r["id"] == id })
}

func (s *rpcSession) probe() probeAnswer {
	s.t.Helper()
	s.send(map[string]any{"type": "prompt", "message": "/probe"})
	record := s.until(func(r map[string]any) bool { return strings.Contains(fmt.Sprint(r["message"]), "PROBE:") })
	var answer probeAnswer
	if err := json.Unmarshal([]byte(strings.SplitN(fmt.Sprint(record["message"]), "PROBE:", 2)[1]), &answer); err != nil {
		s.t.Fatal(err)
	}
	return answer
}

// extensionExecutables lists the executables of pig's descendants that are not helpers: the runtime process of the extension.
func (s *rpcSession) extensionExecutables() []string {
	var exes []string
	for _, pid := range descendants(s.cmd.Process.Pid) {
		if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
			exes = append(exes, exe)
		}
	}
	return exes
}

func descendants(root int) []int {
	children := map[int][]int{}
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		var pid int
		if _, err := fmt.Sscanf(entry.Name(), "%d", &pid); err != nil {
			continue
		}
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		text := string(data)
		fields := strings.Fields(text[strings.LastIndex(text, ")")+2:])
		var ppid int
		_, _ = fmt.Sscanf(fields[1], "%d", &ppid)
		children[ppid] = append(children[ppid], pid)
	}
	var out []int
	todo := []int{root}
	for len(todo) > 0 {
		next := todo[0]
		todo = todo[1:]
		for _, child := range children[next] {
			out = append(out, child)
			todo = append(todo, child)
		}
	}
	return out
}

func (e *e2e) status() string {
	e.t.Helper()
	cmd := exec.Command(e.pig, "extensions", "runtime", "status")
	cmd.Env = e.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("status: %v\n%s", err, out)
	}
	return string(out)
}

// buildPending runs the worker until it has handled every pending request, as the background loop does between polls.
func (e *e2e) buildPending() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	e.worker.tick(ctx)
}

func hasExe(exes []string, suffix string) bool {
	for _, exe := range exes {
		if strings.HasSuffix(exe, suffix) {
			return true
		}
	}
	return false
}

// Gate G-UX: from the user's side nothing changes except speed.
//
//  1. The first session runs on Node (the Piglet has not installed its manifest), the second on Bun while the set compiles in the background, the
//     third on the compiled host.
//  2. /reload after an extension source change switches that session to the Bun tier at once, with the new source; the rebuild publishes an image
//     and the next start runs it.
//  3. process.execPath names a Node in every tier.
//  4. The first build is silent: nothing on stderr, no notification event, and no prompt.
func TestGateUXFirstBuildReloadAndExecPath(t *testing.T) {
	e := newE2E(t)
	entry := e.writeExtension("v1", false)

	// Session 1: no manifest yet: Node. The worker installs the manifest and the compiler (a silent download).
	s1 := e.start(entry)
	s1.ready()
	first := s1.probe()
	if first.Version != "v1" || first.Bun || filepath.Base(first.ExecPath) != "node" {
		t.Fatalf("session 1 (no manifest): %+v", first)
	}
	s1.close()
	e.buildPending()
	if _, err := os.Stat(e.worker.store.manifestPath()); err != nil {
		t.Fatalf("the worker wrote no manifest: %v", err)
	}

	// Session 2: the manifest exists and no image: the set runs on Bun and a build request is recorded; the worker builds while the session runs.
	s2 := e.start(entry)
	s2.ready()
	second := s2.probe()
	if second.Version != "v1" || !second.Bun {
		t.Fatalf("session 2 (building): %+v, want Bun", second)
	}
	requests, _ := e.worker.store.readRequests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d after a launch with no image", len(requests))
	}
	start := time.Now()
	e.buildPending() // runs while session 2 is up
	t.Logf("first build took %s", time.Since(start).Round(time.Second))
	again := s2.probe()
	if again.Pid != second.Pid || again.Version != "v1" {
		t.Errorf("the background build disturbed the running session: %+v then %+v", second, again)
	}
	if stderr := s2.err.String(); strings.Contains(strings.ToLower(stderr), "compil") || strings.Contains(strings.ToLower(stderr), "aot") {
		t.Errorf("the build was not silent: stderr %q", stderr)
	}
	s2.close()

	// Session 3: the compiled host.
	s3 := e.start(entry)
	s3.ready()
	third := s3.probe()
	if third.Version != "v1" || !hasExe(s3.extensionExecutables(), "/pig-ts-host") {
		t.Fatalf("session 3: %+v, executables %v; want the compiled host", third, s3.extensionExecutables())
	}
	// process.execPath is what Node reports for itself, the value the Node tier gave in session 1, whatever launcher the PATH entry is.
	if third.ExecPath != first.ExecPath {
		t.Errorf("process.execPath = %q on the compiled host, %q on Node", third.ExecPath, first.ExecPath)
	}
	if reloaded := third; reloaded.ExecPath == second.ExecPath {
		t.Errorf("the Bun tier and the compiled host report the same process.execPath %q: the test cannot tell them apart", second.ExecPath)
	}
	t.Logf("observables: node tier %+v; bun tier %+v; compiled host %+v", first, second, third)
	if third.Argv0 != first.Argv0 {
		t.Errorf("process.argv[0] = %q on the compiled host, %q on Node", third.Argv0, first.Argv0)
	}
	if third.Bun != true { // the compiled host is a Bun; extensions see Bun globals as they do on the Bun tier
		t.Logf("Bun global not visible on the compiled host: %+v", third)
	}

	// /reload after a source change: the session moves to the Bun tier with the new source at once.
	e.writeExtension("v2", false)
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(entry, future, future)
	// The command's response arrives when its handler, and so the reload, has finished. A prompt sent before that can wedge the session, with any runtime.
	reloadID := s3.send(map[string]any{"type": "prompt", "message": "/doreload"})
	s3.until(func(r map[string]any) bool { return r["type"] == "response" && r["id"] == reloadID })
	reloaded := s3.probe()
	if reloaded.Version != "v2" || !reloaded.Bun || reloaded.Pid == third.Pid {
		t.Fatalf("after /reload with an edited source: %+v (before %+v), want v2 on Bun in a new process", reloaded, third)
	}
	requests, _ = e.worker.store.readRequests()
	if len(requests) != 1 {
		t.Fatalf("requests after the reload = %d, want a rebuild request", len(requests))
	}
	e.buildPending()
	s3.close()

	// Next start: the rebuilt image.
	s4 := e.start(entry)
	s4.ready()
	fourth := s4.probe()
	if fourth.Version != "v2" || !hasExe(s4.extensionExecutables(), "/pig-ts-host") {
		t.Fatalf("session 4: %+v, executables %v; want the rebuilt compiled host", fourth, s4.extensionExecutables())
	}
}

// Gate G-UX 2: an extension that uses a Node-only API loads on Node, with the reason in the status, and the next start does not retry the other tiers.
func TestGateUXNodeOnlyExtensionRunsOnNode(t *testing.T) {
	e := newE2E(t)
	entry := e.writeExtension("node-only", true)
	if err := e.worker.store.ensureManifest(); err != nil {
		t.Fatal(err)
	}
	s := e.start(entry)
	s.ready()
	answer := s.probe()
	if answer.Bun || filepath.Base(answer.ExecPath) != "node" {
		t.Fatalf("the Node-only extension ran on %+v, want Node", answer)
	}
	s.close()
	status := e.status()
	for _, want := range []string{"Node-only extension sets", "not yet implemented"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	// The next start goes to Node without trying the compiled host again.
	s2 := e.start(entry)
	s2.ready()
	if again := s2.probe(); again.Bun || filepath.Base(again.ExecPath) != "node" {
		t.Fatalf("second start ran on %+v", again)
	}
	if hasExe(s2.extensionExecutables(), "/bun") {
		t.Errorf("the second start started Bun: %v", s2.extensionExecutables())
	}
}
