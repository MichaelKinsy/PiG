package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Each fixture owns its sources, trace, Host, and recovery processes. Equal member names in concurrent fixtures still use the Host's private socket directory.
func nodeRecoveryFixture(t *testing.T, count int, timerCrash ...bool) (*Host, []ExtConfig, string) {
	t.Helper()
	nodeCellRequireNode(t)
	root := t.TempDir()
	trace := filepath.Join(root, "invocations")
	var configs []ExtConfig
	for i := range count {
		name := fmt.Sprintf("member%d", i)
		entry := filepath.Join(root, name+".mjs")
		source := fmt.Sprintf(`import {appendFileSync} from "node:fs";
export default function(pi) {
 pi.events.on("ping", data=>data.push(%q+"@"+process.pid));
 pi.on("session_start",()=>{});
 pi.registerTool({name:%q,label:"probe",description:"probe",parameters:{type:"object",properties:{}},execute:async(_id,args)=>{
  if(args.crash){appendFileSync(%q,"crash\n");process.exit(23);}
  const heard=[];pi.events.emit("ping",heard);return {content:[{type:"text",text:JSON.stringify(heard)}]};
 }});
}`, name, name, trace)
		if len(timerCrash) > 0 && timerCrash[0] && i == 1 {
			source = `import {existsSync, unlinkSync} from "node:fs";` + source
			source += fmt.Sprintf(`
setInterval(() => { if (existsSync(%q)) { unlinkSync(%q); throw new Error("timer-owned crash"); } }, 10).unref();
`, filepath.Join(root, "trigger"), filepath.Join(root, "trigger"))
		}
		if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"})
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	return h, configs, trace
}

var crashNoticeLogs sync.Map // *Host -> *crashNotices

type crashNotices struct {
	mu      sync.Mutex
	notices []string
}

var stderrLogInNotice = regexp.MustCompile(`\(stderr: (.+)\)$`)

// recordCrashNotices keeps the host's crash notices, the only surface that
// reports a failed automatic recovery, for waitRecovered's failure report.
// Reported notices retain their stderr logs; the test removes them.
func recordCrashNotices(t *testing.T, h *Host) {
	t.Helper()
	log := &crashNotices{}
	crashNoticeLogs.Store(h, log)
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		log.mu.Lock()
		defer log.mu.Unlock()
		log.notices = append(log.notices, fmt.Sprintf("%s delay=%v disabled=%t: %s", name, delay, disabled, reason))
	})
	t.Cleanup(func() {
		crashNoticeLogs.Delete(h)
		log.mu.Lock()
		defer log.mu.Unlock()
		for _, notice := range log.notices {
			if match := stderrLogInNotice.FindStringSubmatch(notice); match != nil {
				_ = os.Remove(match[1])
			}
		}
	})
}

func waitRecovered(t *testing.T, h *Host, names []string, oldPID int) {
	t.Helper()
	recovered := func() (bool, []string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		ok := true
		var state []string
		for _, name := range names {
			me := h.exts[name]
			switch {
			case me == nil:
				ok = false
				state = append(state, name+": not registered")
			case pidOf(me) == 0:
				ok = false
				state = append(state, name+": no process")
			case pidOf(me) == oldPID:
				ok = false
				state = append(state, fmt.Sprintf("%s: still pid %d", name, oldPID))
			case me.connection().closed.Load():
				ok = false
				state = append(state, fmt.Sprintf("%s: pid %d, connection closed", name, pidOf(me)))
			default:
				state = append(state, fmt.Sprintf("%s: pid %d", name, pidOf(me)))
			}
		}
		return ok, state
	}
	// Recovery restarts Node processes, so a wait that a loaded machine stretches past a fixed bound is still recovering; the test budget bounds a real hang below the package deadline.
	wait := testbudget.Wait(t)
	deadline := time.Now().Add(wait)
	for sleep := 10 * time.Millisecond; ; sleep = min(sleep*2, 200*time.Millisecond) {
		ok, state := recovered()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			var notices []string
			if log, found := crashNoticeLogs.Load(h); found {
				log := log.(*crashNotices)
				log.mu.Lock()
				notices = append(notices, log.notices...)
				log.mu.Unlock()
			}
			t.Fatalf("Node members did not recover within %s:\n  %s\ncrash notices:\n  %s\nhost goroutines:\n%s",
				wait, strings.Join(state, "\n  "), strings.Join(notices, "\n  "), hostGoroutines())
		}
		time.Sleep(sleep)
	}
}

// hostGoroutines returns the stacks of goroutines inside this package's
// non-test code, which shows where a recovery that never finished is waiting.
func hostGoroutines() string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for stack := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(stack, "coding/extension/host/subprocess.(") && !strings.Contains(stack, "subprocess.waitRecovered") {
			out = append(out, stack)
		}
	}
	return strings.Join(out, "\n\n")
}

// recoveryBus emits on the shared bus from name and returns every listener that heard it, in dispatch order, and the members grouped by the process that hosts them. Every Node realm shares one bus, so all members hear; the process groups show the recovery placement.
func recoveryBus(t *testing.T, h *Host, name string) string {
	t.Helper()
	h.mu.Lock()
	me := h.exts[name]
	h.mu.Unlock()
	if me == nil || me.ext == nil {
		t.Fatalf("%s is not registered", name)
	}
	result, err := me.ext.Tools[name].Definition.Execute(t.Context(), "probe", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	return recoveryBusPlacement(t, result.(agent.AgentToolResult).Text())
}

// recoveryBusPlacement renders a ping result as the listeners in dispatch order and the members grouped by hosting process.
func recoveryBusPlacement(t *testing.T, text string) string {
	t.Helper()
	var heard []string
	if err := json.Unmarshal([]byte(text), &heard); err != nil {
		t.Fatal(err)
	}
	var names []string
	groups := map[string][]string{}
	var order []string
	for _, entry := range heard {
		member, pid, _ := strings.Cut(entry, "@")
		names = append(names, member)
		if groups[pid] == nil {
			order = append(order, pid)
		}
		groups[pid] = append(groups[pid], member)
	}
	var placement []string
	for _, pid := range order {
		placement = append(placement, strings.Join(groups[pid], "+"))
	}
	return strings.Join(names, ",") + " | " + strings.Join(placement, " ")
}

func TestNodeCrashQuarantinesOnlyCulpritAndDoesNotReplay(t *testing.T) {
	t.Parallel()
	h, configs, trace := nodeRecoveryFixture(t, 3)
	h.mu.Lock()
	bad := h.exts["member1"]
	oldPID := pidOf(bad)
	staleHandler := bad.ext.EventHandlers("session_start")[0]
	original := h.exts["member0"].ext.Tools["member0"].Definition.Execute
	h.mu.Unlock()
	_, err := bad.ext.Tools["member1"].Definition.Execute(t.Context(), "interrupted", json.RawMessage(`{"crash":true}`), nil)
	if err == nil {
		t.Fatal("interrupted callback succeeded")
	}
	waitRecovered(t, h, []string{"member0", "member1", "member2"}, oldPID)
	if got := recoveryBus(t, h, "member0"); got != "member0,member1,member2 | member0+member2 member1" {
		t.Fatalf("healthy bus split: %s", got)
	}
	h.mu.Lock()
	a, b, c := pidOf(h.exts["member0"]), pidOf(h.exts["member1"]), pidOf(h.exts["member2"])
	h.mu.Unlock()
	if a != c || a == b {
		t.Fatalf("recovery placement: %d %d %d", a, b, c)
	}
	result, err := original(context.Background(), "new-call", json.RawMessage(`{}`), nil)
	if err != nil || recoveryBusPlacement(t, result.(agent.AgentToolResult).Text()) != "member0,member1,member2 | member0+member2 member1" {
		t.Fatalf("registered tool did not recover: %v %v", result, err)
	}
	if _, err := staleHandler(); err == nil {
		t.Fatal("old-generation callback reached the recovered process")
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "crash\n") != 1 {
		t.Fatalf("interrupted callback replayed: %s", data)
	}
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := recoveryBus(t, h, "member0"); got != "member0,member1,member2 | member0+member2 member1" {
		t.Fatalf("reload split healthy members: %s", got)
	}
	report := h.LastReloadReport()
	found := false
	for _, cell := range report.Cells {
		if cell.Quarantined && len(cell.Extensions) == 1 && cell.Extensions[0] == "member1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reload report omitted culprit quarantine: %+v", report.Cells)
	}
}
func TestNodeRepeatedUnknownCrashBisectsAndRejoinsHealthyMembers(t *testing.T) {
	t.Parallel()
	h, _, _ := nodeRecoveryFixture(t, 4)
	for round := range 4 {
		h.mu.Lock()
		pid := pidOf(h.exts["member2"])
		h.mu.Unlock()
		if err := killTestProcess(pid); err != nil {
			t.Fatal(err)
		}
		waitRecovered(t, h, []string{"member0", "member1", "member2", "member3"}, pid)
		h.mu.Lock()
		pids := map[int]bool{}
		for _, me := range h.exts {
			pids[pidOf(me)] = true
		}
		h.mu.Unlock()
		want := []int{1, 2, 3, 2}[round]
		if len(pids) != want {
			t.Fatalf("diagnostic round %d: %d processes, want %d (whole retry, halves, narrowed halves, culprit plus healthy)", round, len(pids), want)
		}
	}
	if got := recoveryBus(t, h, "member0"); got != "member0,member1,member2,member3 | member0+member1+member3 member2" {
		t.Fatalf("healthy members never rejoined: %s", got)
	}
}

func TestNodeFactoryCrashKeepsHealthyMembersTogether(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 3 {
		name := fmt.Sprintf("factory%d", i)
		entry := filepath.Join(root, name+".mjs")
		crash := ""
		if i == 1 {
			crash = fmt.Sprintf(`if (!existsSync(%q)) { writeFileSync(%q,"once"); process.exit(29); }`, filepath.Join(root, "crashed"), filepath.Join(root, "crashed"))
		}
		source := fmt.Sprintf(`import {existsSync,writeFileSync} from "node:fs";
export default function(pi) { %s
 pi.events.on("ping", x=>x.push(%q+"@"+process.pid));
 pi.registerTool({name:%q,label:"probe",description:"probe",parameters:{type:"object",properties:{}},execute:async()=>{const x=[];pi.events.emit("ping",x);return {content:[{type:"text",text:JSON.stringify(x)}]};}});
}`, crash, name, name)
		if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test complete") })
	recordCrashNotices(t, h)
	_, errs := h.LoadAll(t.Context(), configs)
	if len(errs) == 0 {
		t.Fatal("factory process death was not surfaced")
	}
	waitRecovered(t, h, []string{"factory0", "factory1", "factory2"}, 0)
	if got := recoveryBus(t, h, "factory0"); got != "factory0,factory1,factory2 | factory0+factory2 factory1" {
		t.Fatalf("factory culprit misattributed: %s", got)
	}
}

func TestNodeTimerCrashUsesRuntimeStackAttribution(t *testing.T) {
	t.Parallel()
	h, configs, _ := nodeRecoveryFixture(t, 3, true)
	h.mu.Lock()
	pid := pidOf(h.exts["member0"])
	h.mu.Unlock()
	if err := os.WriteFile(filepath.Join(filepath.Dir(configs[0].Source), "trigger"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitRecovered(t, h, []string{"member0", "member1", "member2"}, pid)
	if got := recoveryBus(t, h, "member0"); got != "member0,member1,member2 | member0+member2 member1" {
		t.Fatalf("timer crash did not isolate stack owner: %s", got)
	}
}

func TestNodeUnattributableCrashRestartsWholeGroup(t *testing.T) {
	t.Parallel()
	h, _, _ := nodeRecoveryFixture(t, 3)
	h.mu.Lock()
	pid := pidOf(h.exts["member0"])
	h.mu.Unlock()
	if err := killTestProcess(pid); err != nil {
		t.Fatal(err)
	}
	waitRecovered(t, h, []string{"member0", "member1", "member2"}, pid)
	if got := recoveryBus(t, h, "member0"); got != "member0,member1,member2 | member0+member1+member2" {
		t.Fatalf("unattributable first crash split bus: %s", got)
	}
}

// pidOf returns the extension's current process id, or 0 when it has none, through the same lock the host restarts a process under.
func pidOf(me *managedExt) int {
	if proc, _ := me.processHandle(); proc != nil {
		return proc.Pid
	}
	return 0
}
