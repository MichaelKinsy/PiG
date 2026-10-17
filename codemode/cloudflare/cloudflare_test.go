package cloudflare_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/codemode/cloudflare"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// objectRemote lets a RemoteSandbox use an object as its remote, as it would use a Durable Object stub.
type objectRemote struct {
	object *cloudflare.SandboxDurableObject
}

func (r objectRemote) Execute(ctx context.Context, request codemode.RemoteRequest, exchange codemode.RemoteExchange) error {
	return r.object.Execute(ctx, request, exchange)
}

func execute(t *testing.T, object *cloudflare.SandboxDurableObject, code string, options codemode.RemoteSandboxOptions) codemode.Result {
	t.Helper()
	options.Remote = func() (codemode.Remote, error) { return objectRemote{object}, nil }
	options.TimeoutMs = 60_000
	sandbox, err := codemode.NewRemoteSandbox(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	result, err := sandbox.Execute(t.Context(), code, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The object's limits cap every request: a request asks for less, never more (CodemodeSandboxDurableObject.execute passes
// maxInterruptBudget and maxMemoryLimitBytes to serveCodemodeRemote). Pi: packages/codemode/src/cloudflare.ts:68-72 (execute).
func TestTheObjectsLimitsCapEveryRequest(t *testing.T) {
	object := &cloudflare.SandboxDurableObject{MaxInterruptBudget: 1000, MaxMemoryLimitBytes: 4 << 20}
	budget := execute(t, object, "while (true) {}", codemode.RemoteSandboxOptions{InterruptBudget: 1_000_000_000})
	if budget.OK || budget.Error.Kind != codemode.ErrorTimeout || budget.Error.Message != "Execution exceeded its interrupt budget of 1000" {
		t.Fatalf("budget: %+v", budget)
	}
	hold := "const parts = []; for (let i = 0; i < 6; i++) parts.push('x'.repeat(1 << 20) + i); return parts.length"
	capped := execute(t, object, hold, codemode.RemoteSandboxOptions{MemoryLimitBytes: 1 << 30})
	if capped.OK || !strings.Contains(capped.Error.Message+capped.Error.Name, "out of memory") {
		t.Fatalf("a request cannot raise the object's memory limit: %+v", capped)
	}
	roomy := execute(t, &cloudflare.SandboxDurableObject{MaxMemoryLimitBytes: 64 << 20}, hold, codemode.RemoteSandboxOptions{})
	if !roomy.OK || string(roomy.Value) != "6" {
		t.Fatalf("under the limit: %+v", roomy)
	}
}

func TestNewSandboxDurableObjectHasPisDefaults(t *testing.T) {
	object := cloudflare.NewSandboxDurableObject()
	if object.MaxInterruptBudget != codemode.DefaultInterruptBudget || object.MaxMemoryLimitBytes != cloudflare.DefaultRemoteMemoryLimitBytes {
		t.Fatalf("%+v", object)
	}
	// A zero object serves with the same defaults: 20 MiB of strings fit the 32 MiB default and 40 MiB do not.
	hold := func(mib int) string {
		return "const parts = []; for (let i = 0; i < " + strconv.Itoa(mib) + "; i++) parts.push('x'.repeat(1 << 20) + i); return parts.length"
	}
	if r := execute(t, &cloudflare.SandboxDurableObject{}, hold(20), codemode.RemoteSandboxOptions{}); !r.OK {
		t.Fatalf("%+v", r)
	}
	if r := execute(t, &cloudflare.SandboxDurableObject{}, hold(40), codemode.RemoteSandboxOptions{}); r.OK || !strings.Contains(r.Error.Message, "out of memory") {
		t.Fatalf("the default memory limit is 32 MiB: %+v", r)
	}
}

// createInlineCodemodeSandbox spreads the options and then sets wasm to the entry's module (packages/codemode/src/cloudflare.ts:38-40),
// so a module the caller passes is replaced, never loaded.
func TestCreateInlineSandboxRunsWithTheEntrysModule(t *testing.T) {
	callers := 0
	sandbox, err := cloudflare.CreateInlineSandbox(codemode.InlineSandboxOptions{InterruptBudget: 1000, SandboxOptions: codemode.SandboxOptions{
		Wasm: func() ([]byte, error) { callers++; return nil, errors.New("the caller's module") },
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	r, err := sandbox.Execute(t.Context(), "while (true) {}", codemode.ExecuteOptions{TimeoutMs: 600_000})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || r.Error.Kind != codemode.ErrorTimeout {
		t.Fatalf("%+v", r)
	}
	if callers != 0 {
		t.Fatalf("the caller's module was loaded %d times, want 0", callers)
	}
	if len(cloudflare.QuickJSWasm()) == 0 || &cloudflare.QuickJSWasm()[0] != &codemode.QuickJSWasm()[0] {
		t.Fatal("the entry's module is the embedded one")
	}
}

// The constants of the Go entry and of the Worker object are one contract (sandbox-object.ts).
func TestTheWorkerObjectHasTheSameDefaults(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(testenv.ModuleRoot(t), "durable", "core", "host", "cf", "src", "codemode", "sandbox-object.ts"))
	if err != nil {
		t.Fatal(err)
	}
	find := func(name, pattern string) int {
		m := regexp.MustCompile(`export const ` + name + ` = ` + pattern).FindSubmatch(source)
		if m == nil {
			t.Fatalf("sandbox-object.ts has no %s", name)
		}
		n := 1
		for part := range strings.SplitSeq(strings.ReplaceAll(string(m[1]), "_", ""), "*") {
			v, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				t.Fatal(err)
			}
			n *= v
		}
		return n
	}
	if got := find("DEFAULT_INTERRUPT_BUDGET", `([0-9_]+)`); got != codemode.DefaultInterruptBudget {
		t.Fatalf("DEFAULT_INTERRUPT_BUDGET %d, Go %d", got, codemode.DefaultInterruptBudget)
	}
	if got := find("DEFAULT_REMOTE_MEMORY_LIMIT_BYTES", `([0-9_ *]+)`); uint32(got) != cloudflare.DefaultRemoteMemoryLimitBytes {
		t.Fatalf("DEFAULT_REMOTE_MEMORY_LIMIT_BYTES %d, Go %d", got, cloudflare.DefaultRemoteMemoryLimitBytes)
	}
}

// The object's execute() called directly, as a client of the Durable Object calls it over Workers RPC: it runs the request's script and
// reports through the exchange, in order, until the terminal message has been delivered, then returns. Pi: packages/codemode/src/cloudflare.ts:68-72
// (CodemodeSandboxDurableObject.execute) and packages/codemode/src/runtime/remote.ts:62-67 (the exchange).
func TestTheObjectExecutesTheRequestAndReportsThroughTheExchange(t *testing.T) {
	object := cloudflare.NewSandboxDurableObject()
	request := codemode.RemoteRequest{
		Data: codemode.VmStartData{Code: "text('hi'); return 6 * 7", Tools: []codemode.VmStartTool{}, Globals: []codemode.VmStartGlobal{}, Store: map[string]string{}},
	}
	var types []string
	var value *string
	exchange := func(_ context.Context, messages []codemode.RemoteMessage, reply func(*codemode.RemoteReply, error)) {
		for _, m := range messages {
			types = append(types, m.Type)
			if m.Type == codemode.MessageDone && m.Ok {
				value = m.Value
			}
		}
		reply(nil, nil)
	}
	if err := object.Execute(t.Context(), request, exchange); err != nil {
		t.Fatal(err)
	}
	if strings.Join(types, ",") != "output,done" || value == nil || *value != "42" {
		t.Fatalf("messages = %v, value = %v", types, value)
	}
}
