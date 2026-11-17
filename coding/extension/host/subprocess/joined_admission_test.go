package subprocess

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// loggedGoFactory writes a Go factory extension that appends "<name> <pid>" to logPath each time its factory runs, and returns its config. The probe tool reports the process id.
func loggedGoFactory(t *testing.T, name, logPath string) ExtConfig {
	t.Helper()
	dir := t.TempDir()
	modulePath := "example.com/logged" + name
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package ext

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	if f, err := os.OpenFile(LOGPATH, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "%s %d\n", EXTENSION_NAME, os.Getpid())
		f.Close()
	}
	e := sdk.New(EXTENSION_NAME)
	e.Tool("probe", "probe", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]string{"content": fmt.Sprintf("{\"calls\":1,\"pid\":%d}", os.Getpid())}, nil
	})
	return e
}
`
	source = strings.ReplaceAll(source, "EXTENSION_NAME", strconv.Quote(name))
	source = strings.Replace(source, "LOGPATH", strconv.Quote(logPath), 1)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return packedFactoryConfig(name, dir, modulePath, "joined-go-"+name)
}

// failAdmit fails the n-th (1-based) admit line written to a process and passes every other line, the noop probe of the registry included.
type failAdmit struct {
	io.WriteCloser
	mu     sync.Mutex
	n      int
	admits int
}

func (w *failAdmit) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), "admit\t") {
		w.mu.Lock()
		w.admits++
		fail := w.admits == w.n
		w.mu.Unlock()
		if fail {
			return 0, errors.New("injected: the admission channel is broken")
		}
	}
	return w.WriteCloser.Write(p)
}

func (w *failAdmit) attempts() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.admits
}

// joinedTwoMemberCells loads two Go factory members, one cell, on Host A and breaks the n-th admission line the process of that cell takes next, which is the one Host B writes when it joins. It returns B's load errors, the log of factory runs ("<member> <pid>" lines), A's process id, and the broken channel.
func joinedTwoMemberCells(t *testing.T, failing int) (b *Host, a *Host, errs []error, runs []string, pid int, channel *failAdmit) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "factory-runs.log")
	configs := []ExtConfig{loggedGoFactory(t, "joined-one", log), loggedGoFactory(t, "joined-two", log)}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	a, b = NewHost(cwd), NewHost(cwd)
	a.ShareCells(registry)
	b.ShareCells(registry)
	t.Cleanup(func() { a.Shutdown("test done"); b.Shutdown("test done"); registry.Close() })
	loadShared(t, a, configs)
	share := memberShare(t, a, "joined-one")
	if memberShare(t, a, "joined-two") != share {
		t.Fatal("the two members are not one packed cell")
	}
	pid = retainedProbeOf(t, a, "joined-one").Pid
	channel = &failAdmit{WriteCloser: share.admit, n: failing}
	share.admitMu.Lock()
	share.admit = channel
	share.admitMu.Unlock()
	b.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	_, errs = b.LoadAll(t.Context(), configs)
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return b, a, errs, strings.Split(strings.TrimSpace(string(data)), "\n"), pid, channel
}

// The first admission a Host writes into a process it joined fails: no factory ran there for it, so the cell starts again, whole, on a fresh process, and every member's factory runs once in each process (D232).
func TestJoinedCellStartsAgainWhenItsFirstAdmissionFails(t *testing.T) {
	b, _, errs, runs, pid, channel := joinedTwoMemberCells(t, 1)
	if len(errs) > 0 {
		t.Fatalf("Host B did not start the cell again: %v", errs)
	}
	if channel.attempts() != 1 {
		t.Fatalf("admit lines written to the old process = %d, want the one that failed", channel.attempts())
	}
	one, two := retainedProbeOf(t, b, "joined-one"), retainedProbeOf(t, b, "joined-two")
	if one.Pid == pid || one.Pid != two.Pid || one.Pid == 0 {
		t.Fatalf("Host B runs in %d and %d, want one fresh process other than %d", one.Pid, two.Pid, pid)
	}
	want := []string{"joined-one " + strconv.Itoa(pid), "joined-two " + strconv.Itoa(pid), "joined-one " + strconv.Itoa(one.Pid), "joined-two " + strconv.Itoa(one.Pid)}
	if !slices.Equal(runs, want) {
		t.Fatalf("factory runs = %q, want %q", runs, want)
	}
}

// A later admission fails after the earlier member's factory ran in the process Host B joined: the cell does not start again, because that factory would run twice. The failing member is B's load error, the earlier one keeps serving from the shared process, and each factory has run exactly as often as it was admitted (D232).
func TestJoinedCellKeepsTheEarlierMembersWhenALaterAdmissionFails(t *testing.T) {
	b, a, errs, runs, pid, channel := joinedTwoMemberCells(t, 2)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "admit_failed") || !strings.Contains(errs[0].Error(), "joined-two") {
		t.Fatalf("Host B errors = %v, want the one admit_failed of joined-two", errs)
	}
	if channel.attempts() != 2 {
		t.Fatalf("admit lines written to the shared process = %d, want 2", channel.attempts())
	}
	if got := retainedProbeOf(t, b, "joined-one").Pid; got != pid {
		t.Fatalf("Host B's first member runs in %d, want the shared process %d", got, pid)
	}
	if got := retainedProbeOf(t, a, "joined-two").Pid; got != pid {
		t.Fatalf("Host A's member runs in %d, want the shared process %d still alive", got, pid)
	}
	p := strconv.Itoa(pid)
	want := []string{"joined-one " + p, "joined-two " + p, "joined-one " + p}
	if !slices.Equal(runs, want) {
		t.Fatalf("factory runs = %q, want %q", runs, want)
	}
	for _, ext := range b.Extensions() {
		if ext.Name == "joined-two" {
			t.Fatal("Host B holds the member whose admission failed")
		}
	}
}
