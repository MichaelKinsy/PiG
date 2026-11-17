package subprocess

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// retainedBusMembers is the size of the reporter's reproduction (issue 486): enough members that the runner's goroutines are
// unlikely to take the factory turn in admission order.
const retainedBusMembers = 8

// A member's factory subscribes to the shared event bus before it returns. Package state outlives each factory call, as it does in
// Pi's one runtime, so the tool reports how many times the factory ran, how often this member's listener heard the channel and the
// process that holds both.
const retainedBusSource = `package ext

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

const generation = GENERATION

var factoryCalls, heard atomic.Int64

func Extension() *sdk.Extension {
	factoryCalls.Add(1)
	e := sdk.New(EXTENSION_NAME)
	if _, err := e.Events().On("retained-bus:ping", func(_ sdk.Context, _ any) error {
		heard.Add(1)
		return nil
	}); err != nil {
		panic(err)
	}
	e.Tool("probe", "probe", sdk.Schema{"type": "object"}, func(_ sdk.Context, _ map[string]any) (any, error) {
		return map[string]string{"content": fmt.Sprintf("{\"calls\":%d,\"heard\":%d,\"pid\":%d,\"generation\":%d}", factoryCalls.Load(), heard.Load(), os.Getpid(), generation)}, nil
	})
	e.Tool("ping", "ping", sdk.Schema{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.Events().Emit("retained-bus:ping", map[string]any{"from": EXTENSION_NAME}); err != nil {
			return nil, err
		}
		return map[string]string{"content": "{}"}, nil
	})
	return e
}
`

type retainedBusProbe struct {
	Calls      int `json:"calls"`
	Heard      int `json:"heard"`
	Pid        int `json:"pid"`
	Generation int `json:"generation"`
}

// writeRetainedBusMember writes the member's module with the given source generation and returns its configuration. Rewriting the
// module with another generation changes the member's content, as an edit does.
func writeRetainedBusMember(t *testing.T, name, dir string, generation int) ExtConfig {
	t.Helper()
	modulePath := "example.com/retainedbus/" + name
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	source := strings.NewReplacer("EXTENSION_NAME", fmt.Sprintf("%q", name), "GENERATION", fmt.Sprint(generation)).Replace(retainedBusSource)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return packedFactoryConfig(name, dir, modulePath, fmt.Sprintf("%s-%d", name, generation))
}

func retainedBusConfigs(t *testing.T, count int) []ExtConfig {
	t.Helper()
	configs := make([]ExtConfig, 0, count)
	for i := range count {
		configs = append(configs, writeRetainedBusMember(t, fmt.Sprintf("bus-member-%d", i), t.TempDir(), 0))
	}
	return configs
}

func retainedBusToolOf(t *testing.T, h *Host, name, tool string) retainedBusProbe {
	t.Helper()
	for _, ext := range h.Extensions() {
		if ext.Name != name {
			continue
		}
		result, err := ext.Tools[tool].Definition.Execute(t.Context(), tool+"-call", json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatalf("%s %s: %v", tool, name, err)
		}
		var probe retainedBusProbe
		if err := json.Unmarshal([]byte(result.Text()), &probe); err != nil {
			t.Fatal(err)
		}
		return probe
	}
	t.Fatalf("extension %s is not loaded", name)
	return retainedBusProbe{}
}

// reloadWithinBudget runs Host.Reload and fails the test, with the goroutine dump of the packed runners, when it does not return
// within the budget for a reload that waits on nothing.
func reloadWithinBudget(t *testing.T, h *Host) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := h.Reload(t.Context())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("Reload did not return: the host waits for a member that cannot finish its factory\n%s", packedRunnerStacks(t, h))
	}
}

// packedRunnerStacks returns the goroutine dumps of the host (the goroutines in Reload) and of every packed runner, which each writes
// to its log on SIGQUIT.
func packedRunnerStacks(t *testing.T, h *Host) string {
	t.Helper()
	var dump strings.Builder
	var host bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&host, 2)
	dump.WriteString("host goroutines in Reload or accept:\n")
	for block := range strings.SplitSeq(host.String(), "\n\n") {
		if strings.Contains(block, "(*Host).Reload") || strings.Contains(block, "acceptPackedExt") {
			dump.WriteString(block + "\n\n")
		}
	}
	h.mu.Lock()
	var logs []string
	for _, process := range h.packedProcesses {
		if process.cmd != nil && process.cmd.Process != nil && process.stderrLog != nil {
			if err := process.cmd.Process.Signal(syscall.SIGQUIT); err != nil {
				dump.WriteString("signal: " + err.Error() + "\n")
			}
			logs = append(logs, process.stderrLog.path)
		}
	}
	h.mu.Unlock()
	start := time.Now()
	for _, path := range logs {
		// The host removes a runner's log once the runner has exited, so the dump is read as it is written, until it stops growing.
		var data []byte
		stable := time.Now()
		for (len(data) == 0 || time.Since(stable) < 100*time.Millisecond) && time.Since(start) < 10*time.Second {
			if read, err := os.ReadFile(path); err == nil && len(read) > len(data) {
				data, stable = read, time.Now()
			}
			time.Sleep(time.Millisecond)
		}
		dump.WriteString("runner " + path + ":\n" + string(data))
	}
	return dump.String()
}

// Pi runs one extension's factory at a time, and a factory's pi.events.on is an ordinary call on the shared emitter, so every
// subscription a factory makes is live when the factory returns and the next factory starts. A reload reinvokes every member's
// factory in the runner that already holds them: the host serves one member's factory-time call before the runner starts the next
// member's factory, or the runner's factory turn goes to a later member while the host waits for an earlier one to connect.
func TestGoFactoryReloadWithFactoryTimeSubscriptionsCompletesAndDeliversOnce(t *testing.T) {
	configs := retainedBusConfigs(t, retainedBusMembers)
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	first := retainedBusToolOf(t, h, configs[0].Name, "probe")
	for reload := 1; reload <= 3; reload++ {
		reloadWithinBudget(t, h)
		if issues := h.LastReloadReport().Issues; len(issues) != 0 {
			t.Fatalf("reload %d issues = %q, want none", reload, issues)
		}
		retainedBusToolOf(t, h, configs[0].Name, "ping")
		for i, cfg := range configs {
			got := retainedBusToolOf(t, h, cfg.Name, "probe")
			// Each ping reaches the live generation's listener and not a retired generation's: the process counts one hearing per ping.
			if got.Pid != first.Pid || got.Calls != reload+1 || got.Heard != reload {
				t.Fatalf("reload %d member %d probe = %+v, want pid %d, %d factory calls and %d deliveries", reload, i, got, first.Pid, reload+1, reload)
			}
		}
	}
}

// A reload that edits one member keeps the others' runner: the edited member's cell is built and started afresh, and the cell of the
// unchanged members re-invokes their factories in the process that holds them. Each pass delivers one ping to the live listener of
// every member, the edited member's new instance among them.
func TestGoFactoryReloadRetainedMembersBesideAChangedMemberDeliverOnce(t *testing.T) {
	configs := retainedBusConfigs(t, retainedBusMembers)
	changedDir := t.TempDir()
	changed := writeRetainedBusMember(t, "bus-changed", changedDir, 1)
	changed.Isolation = "isolated"
	all := append(slices.Clone(configs), changed)
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return all, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, errs := h.LoadAll(t.Context(), all); len(errs) > 0 {
		t.Fatal(errs)
	}
	first := retainedBusToolOf(t, h, configs[0].Name, "probe")
	changedPid := retainedBusToolOf(t, h, changed.Name, "probe").Pid
	for reload := 1; reload <= 2; reload++ {
		edited := writeRetainedBusMember(t, "bus-changed", changedDir, reload+1)
		edited.Isolation = "isolated"
		all[len(all)-1] = edited
		reloadWithinBudget(t, h)
		if issues := h.LastReloadReport().Issues; len(issues) != 0 {
			t.Fatalf("reload %d issues = %q, want none", reload, issues)
		}
		retainedBusToolOf(t, h, configs[0].Name, "ping")
		for i, cfg := range configs {
			got := retainedBusToolOf(t, h, cfg.Name, "probe")
			if got.Pid != first.Pid || got.Calls != reload+1 || got.Heard != reload {
				t.Fatalf("reload %d unchanged member %d probe = %+v, want pid %d, %d factory calls and %d deliveries", reload, i, got, first.Pid, reload+1, reload)
			}
		}
		got := retainedBusToolOf(t, h, changed.Name, "probe")
		if got.Generation != reload+1 || got.Heard != 1 || got.Pid == changedPid {
			t.Fatalf("reload %d edited member probe = %+v, want generation %d in a new process (not %d) that heard the ping once", reload, got, reload+1, changedPid)
		}
		changedPid = got.Pid
	}
}
