package subprocess

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// registrationServe is the process half of a fused extension: each connection the host opens to it registers the next payload of regs (the last one
// again once they ran out), then answers pings until the host closes the connection. The first connection ends when crash is closed, as a process
// that crashed.
func registrationServe(crash <-chan struct{}, regs ...*RegisterPayload) func(net.Conn) error {
	var starts atomic.Int32
	return func(conn net.Conn) error {
		n := int(starts.Add(1)) - 1
		if err := writeEnvelopeTo(conn, &Envelope{Type: MsgRegister, Register: regs[min(n, len(regs)-1)]}); err != nil {
			return err
		}
		served := make(chan struct{})
		defer close(served)
		if n == 0 {
			go func() {
				select {
				case <-crash:
					_ = conn.Close()
				case <-served:
				}
			}()
		}
		for {
			env, err := readEnvelopeFrom(conn)
			if err != nil {
				return nil
			}
			if env.Type == MsgPing && env.Ping != nil {
				_ = writeEnvelopeTo(conn, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			}
		}
	}
}

// waitCrashNotice waits for the host to report a crash notice that contains text.
func waitCrashNotice(t *testing.T, h *Host, text string) {
	t.Helper()
	log, _ := crashNoticeLogs.Load(h)
	notices, _ := log.(*crashNotices)
	if notices == nil {
		t.Fatal("the host's crash notices are not recorded")
	}
	deadline := time.Now().Add(testbudget.Wait(t))
	for sleep := time.Millisecond; ; sleep = min(sleep*2, 100*time.Millisecond) {
		notices.mu.Lock()
		found := slices.ContainsFunc(notices.notices, func(notice string) bool { return strings.Contains(notice, text) })
		notices.mu.Unlock()
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no crash notice contains %q", text)
		}
		time.Sleep(sleep)
	}
}

// A restart whose registration fails validation replaces nothing: the host goes on serving the registration of the generation that was running, every
// field of it, not the flag names and requests of the one that was refused.
func TestFailedRegistrationKeepsTheOldRegistration(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	first := &RegisterPayload{
		Name:               "restarter",
		Flags:              []FlagDecl{{Name: "mode", Type: "string", Default: json.RawMessage(`"first"`)}},
		WantsSessionLog:    true,
		WantsModelRegistry: true,
	}
	refused := &RegisterPayload{
		Name:  "restarter",
		Flags: []FlagDecl{{Name: "other", Type: "string"}, {Name: "extra", Type: "string"}},
		// A tool's parameters must be an object schema: the registration is refused after its flags were read.
		Tools: []ToolDecl{{Name: "bad", Parameters: json.RawMessage(`"not a schema"`)}},
	}
	crash := make(chan struct{})
	cfg := ExtConfig{Name: "restarter", Enabled: true, SupervisorConfig: restartSupervisor}
	if _, err := h.LoadInProcess(t.Context(), cfg, registrationServe(crash, first, refused)); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	me := h.exts["restarter"]
	h.mu.Unlock()
	before := registrationOf(me)
	if !slices.Equal(before.flagNames, []string{"mode"}) || !before.wantsSessionLog || !before.wantsModelRegistry {
		t.Fatalf("the first generation registered %+v", before)
	}
	// The process crashes; its supervisor restarts it, and the host refuses the registration of the restart.
	close(crash)
	waitCrashNotice(t, h, "restart failed")
	after := registrationOf(me)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a refused registration changed the registration the host serves:\nbefore %+v\nafter  %+v", before, after)
	}
	if got := h.FlagDefault("restarter", "mode"); got != "first" {
		t.Errorf("flag default after the refused restart = %v, want the first generation's", got)
	}
}

// pushStateTo reads the registration once for a push: the names it asks the bridge for and the session-log request it acts on come from the same
// registration, however a restart lands while the bridge resolves the flags.
func TestStatePushReadsOneRegistration(t *testing.T) {
	rig := newPushRig(t)
	rig.bridge.SetHostAction("getSessionID", func() string { return "pushed" })
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	rig.bridge.SetHostAction("getSessionFile", func() string { return missing })
	setRegistrationFor(rig.me, registrationSeen{flagNames: []string{"first"}, flagDefaults: map[string]any{"first": "one"}})
	next := registrationSeen{flagNames: []string{"second"}, flagDefaults: map[string]any{"second": "two"}, wantsSessionLog: true}
	resolve := rig.bridge.resolveFlag
	var swapped bool
	rig.bridge.resolveFlag = func(extName, name string, override any) any {
		if !swapped {
			// The restart's registration lands while this push resolves its flags.
			swapped = true
			setRegistrationFor(rig.me, next)
		}
		return resolve(extName, name, override)
	}
	rig.push(t)
	if !swapped {
		t.Fatal("the push resolved no flag")
	}
	if rig.h.subscribedToSessionLog("push") {
		t.Error("the push read the names of the first registration and the session-log request of the second")
	}
	rig.push(t)
	if !rig.h.subscribedToSessionLog("push") {
		t.Error("the push after the restart did not use the new registration")
	}
}

// A start replaces the handle of the process an extension runs in and the link to it together: a reader that holds the guard of the handle sees the
// handle and the link of one process, or none, never the link of one with the handle of the other.
func TestProcessLinkChangesWithItsHandle(t *testing.T) {
	type process struct {
		share  *processShare
		owner  *managedExt
		cmd    *exec.Cmd
		exited chan struct{}
	}
	processes := make([]process, 2)
	for i := range processes {
		processes[i] = process{share: &processShare{}, owner: &managedExt{}, cmd: &exec.Cmd{}, exited: make(chan struct{})}
	}
	me := &managedExt{}
	var done atomic.Bool
	var torn atomic.Int64
	var observed atomic.Int64
	var reader sync.WaitGroup
	reader.Go(func() {
		for !done.Load() {
			share, owner, cmd, exited := processLinkOf(me)
			observed.Add(1)
			ok := share == nil && owner == nil && cmd == nil && exited == nil
			for _, p := range processes {
				ok = ok || (share == p.share && owner == p.owner && cmd == p.cmd && exited == (<-chan struct{})(p.exited))
			}
			if !ok {
				torn.Add(1)
			}
			runtime.Gosched()
		}
	})
	for i := range 20000 {
		p := processes[i%len(processes)]
		joinProcessFor(me, p.share, p.owner, p.cmd, p.exited)
		runtime.Gosched()
		clearProcessFor(me)
	}
	done.Store(true)
	reader.Wait()
	if observed.Load() == 0 {
		t.Fatal("the reader never ran")
	}
	if n := torn.Load(); n != 0 {
		t.Errorf("%d of %d reads paired the handle of one process with the link of another", n, observed.Load())
	}
}

// Each benchmark is one reader that runs on every call of an extension, so the cost of reading what a restart replaces is what it adds to a call.
func BenchmarkRegistrationReaders(b *testing.B) {
	h := NewHost(b.TempDir())
	var first *managedExt
	for i := range 64 {
		defaults := map[string]any{"flag": "default"}
		me := &managedExt{host: h, config: ExtConfig{Name: "bench"}}
		setRegistrationFor(me, registrationSeen{flagNames: []string{"flag"}, flagDefaults: defaults})
		if i == 0 {
			first = me
		}
		me.ext = &extension.Extension{}
		h.exts[fmt.Sprintf("bench-%d", i)] = me
	}
	b.Run("flag resolution", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if h.resolveFlagValue("", "flag", nil) != "default" {
				b.Fatal("flag not resolved")
			}
		}
	})
	b.Run("node runtime", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = first.hostsNodeRuntime()
		}
	})
	b.Run("stopped by owner", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = first.stoppedByOwner()
		}
	})
	b.Run("parallel node runtime", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = first.hostsNodeRuntime()
			}
		})
	})
}
