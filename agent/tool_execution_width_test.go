package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// parallelToolWidthEnv makes TestParallelToolBatchRunsAtTheDefaultWidthUnderTheCap's child apply the GOMAXPROCS policy as Main does, run
// a parallel batch of four tools and then a batch of one, and print the default, the cap, the calls the batch ran, the least GOMAXPROCS
// a call of the batch saw, the GOMAXPROCS the lone call saw and the GOMAXPROCS after.
const parallelToolWidthEnv = "PIG_TEST_PARALLEL_TOOL_WIDTH"

// widthTool records the GOMAXPROCS its Execute runs at.
type widthTool struct {
	fakeTool
	mu    *sync.Mutex
	procs *[]int
}

func (t *widthTool) Execute(ctx context.Context, id string, args json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	procs := runtime.GOMAXPROCS(0)
	t.mu.Lock()
	*t.procs = append(*t.procs, procs)
	t.mu.Unlock()
	return t.fakeTool.Execute(ctx, id, args, onUpdate)
}

// D174: the in-process tools of a parallel batch (2 ls + 2 find in a turn) run at the runtime default width under the GOMAXPROCS cap, and
// the cap is back once the batch has settled; a batch of one call stays on the capped Ps. At 897b277d0b the batch ran on the 2 capped Ps
// (E16: 74.2 ms against 67.1 ms uncapped). The child runs without GOMAXPROCS in its environment, so procpolicy.Apply caps it.
func TestParallelToolBatchRunsAtTheDefaultWidthUnderTheCap(t *testing.T) {
	if os.Getenv(parallelToolWidthEnv) == "1" {
		defaultProcs := runtime.GOMAXPROCS(0)
		procpolicy.Apply()
		capped := runtime.GOMAXPROCS(0)
		var mu sync.Mutex
		var batch, lone []int
		send := func(procs *[]int, names ...struct{ id, name string }) {
			tools := make([]AgentTool, len(names))
			for i, n := range names {
				tools[i] = &widthTool{fakeTool: fakeTool{name: n.name, mode: ToolModeParallel, content: "ok"}, mu: &mu, procs: procs}
			}
			a := mustNewAgent(AgentOptions{Model: fakeTestModel(providerFromSeqs(toolCallSeq(names...), textSeq("done"))), Tools: tools, MaxTurns: 5})
			if _, err := a.Send(context.Background(), "width"); err != nil {
				t.Fatalf("Send: %v", err)
			}
		}
		send(&batch, struct{ id, name string }{"tc-0", "ls0"}, struct{ id, name string }{"tc-1", "ls1"},
			struct{ id, name string }{"tc-2", "find0"}, struct{ id, name string }{"tc-3", "find1"})
		send(&lone, struct{ id, name string }{"tc-4", "ls2"})
		least := defaultProcs + 1
		for _, procs := range batch {
			least = min(least, procs)
		}
		loneProcs := -1
		if len(lone) == 1 {
			loneProcs = lone[0]
		}
		fmt.Printf("\nparallel-tool-width %d %d %d %d %d %d\n", defaultProcs, capped, len(batch), least, loneProcs, runtime.GOMAXPROCS(0))
		return
	}
	if testing.Short() {
		t.Skip("applies the GOMAXPROCS policy in a child process")
	}
	command := exec.Command(os.Args[0], "-test.run=^TestParallelToolBatchRunsAtTheDefaultWidthUnderTheCap$")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOMAXPROCS=") {
			command.Env = append(command.Env, kv)
		}
	}
	command.Env = append(command.Env, parallelToolWidthEnv+"=1")
	out, err := command.Output()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	var got []int
	for line := range strings.Lines(string(out)) {
		rest, ok := strings.CutPrefix(line, "parallel-tool-width ")
		if !ok {
			continue
		}
		for field := range strings.FieldsSeq(rest) {
			value, err := strconv.Atoi(field)
			if err != nil {
				t.Fatalf("child line %q: %v", line, err)
			}
			got = append(got, value)
		}
	}
	if len(got) != 6 {
		t.Fatalf("child printed no parallel-tool-width line of six integers:\n%s", out)
	}
	defaultProcs, capped, calls, least, lone, after := got[0], got[1], got[2], got[3], got[4], got[5]
	if defaultProcs <= procpolicy.Cap {
		t.Skipf("the runtime default is %d Ps here, at most the cap %d: nothing to widen", defaultProcs, procpolicy.Cap)
	}
	if capped != procpolicy.Cap {
		t.Fatalf("child capped GOMAXPROCS at %d, want %d", capped, procpolicy.Cap)
	}
	if calls != 4 || least != defaultProcs || lone != procpolicy.Cap || after != procpolicy.Cap {
		t.Fatalf("default %d: the batch ran %d calls at no less than %d Ps, a lone call at %d, %d Ps after; want 4 calls at %d, a lone call at %d, %d after",
			defaultProcs, calls, least, lone, after, defaultProcs, procpolicy.Cap, procpolicy.Cap)
	}
}
