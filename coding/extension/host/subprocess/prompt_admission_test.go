package subprocess

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The first handler body must enter in FIFO order even inside a packed cell.
// Sequence numbers are assigned in the handler before any host call. We do not
// compare notification completion order, which upstream deliberately leaves free.
func TestPackedPromptHandlerBodyFIFO(t *testing.T) {
	const prompts = 512
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			configs := make([]ExtConfig, 0, 2)
			for i := range 2 {
				name := fmt.Sprintf("fifo-%s-%d", language, i)
				module := fmt.Sprintf("fifo_%s_%d", language, i)
				switch language {
				case "go":
					module = "example.com/" + module
					configs = append(configs, packedFactoryConfig(name, writePackedFactoryModule(t, module, name, "probe"), module, name))
				case "python":
					configs = append(configs, packedPythonFactoryConfig(name, writePackedPythonFactoryModule(t, module, name, "probe"), module, name))
				case "rust":
					configs = append(configs, packedRustFactoryConfig(name, writePackedRustFactoryCrate(t, module, name, "probe"), module, name))
				}
			}
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			defer host.Shutdown("test complete")
			records := make(chan string, 2*prompts)
			bridge := NewUIBridge(func() {})
			bridge.SetNotifyFunc(func(message, _ string) { records <- message })
			host.SetUIBridge(bridge)
			loaded, errs := host.LoadAll(t.Context(), configs)
			if len(errs) > 0 || len(loaded) != len(configs) {
				t.Fatalf("LoadAll: %v, %v", loaded, errs)
			}
			a, b := host.exts[configs[0].Name], host.exts[configs[1].Name]
			if a.packedCellKey == "" || a.packedCellKey != b.packedCellKey {
				t.Fatal("fixtures did not share a packed cell")
			}
			// Each logical extension retains its own admission and sequence state.
			for _, ext := range loaded {
				runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
				for i := range prompts {
					runner.BeginUIPrompt(extension.UIPromptKindInput, fmt.Sprintf("fifo:%d", i))()
				}
				seen := make(map[int]bool)
				for range 2 * prompts {
					var record string
					select {
					case record = <-records:
					case <-time.After(10 * time.Second):
						t.Fatal("prompt handler did not run")
					}
					parts := strings.SplitN(record, ":", 3)
					if len(parts) != 3 || parts[0] != "fifo" {
						t.Fatalf("invalid entry %q", record)
					}
					n, err := strconv.Atoi(parts[1])
					if err != nil || n < 0 || n >= 2*prompts || seen[n] {
						t.Fatalf("invalid sequence %q", record)
					}
					seen[n] = true
					kind := "ui_prompt_start"
					if n%2 == 1 {
						kind = "ui_prompt_end"
					}
					want := fmt.Sprintf("%s:fifo:%d", kind, n/2)
					if parts[2] != want {
						t.Fatalf("handler body %d = %s, want %s", n, parts[2], want)
					}
				}
				runner.Invalidate("test complete")
			}
		})
	}
}

// A reported SDK host-call suspension, unlike a socket write, proves that the
// start handler body ran and permits end to enter before start completes.
func TestPromptEndRunsWhileSDKStartWaitsForHost(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	ended := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	ext := sdk.New("prompt-await")
	ext.OnEvent("ui_prompt_start", func(ctx sdk.Context, _ map[string]any) (any, error) {
		err := ctx.WaitForIdle()
		close(finished)
		return nil, err
	})
	ext.OnEvent("ui_prompt_end", func(_ sdk.Context, _ map[string]any) (any, error) { close(ended); return nil, nil })
	host := NewHost(t.TempDir())
	defer host.Shutdown("test complete")
	defer unblock()
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{WaitForIdle: func(context.Context) error { close(entered); <-release; return nil }})
	host.SetUIBridge(bridge)
	loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "prompt-await", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	defer runner.Invalidate("test complete")
	end := runner.BeginUIPrompt(extension.UIPromptKindInput, "pending")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("start handler did not enter")
	}
	end()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("end waited for start completion")
	}
	select {
	case <-finished:
		t.Fatal("start completed while its host call was pending")
	default:
	}
	unblock()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("start did not drain")
	}
}

func TestNodePromptEndRunsWhileStartPromisePending(t *testing.T) {
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	defer host.Shutdown("test complete")
	records := make(chan string, 3)
	bridge := NewUIBridge(func() {})
	bridge.SetNotifyFunc(func(message, _ string) { records <- message })
	host.SetUIBridge(bridge)
	config := startupNodeFixture(t, t.TempDir(), "prompt-promise", `export default function(pi) {
 let release;
 pi.on("ui_prompt_start",async (_,ctx)=>{ await new Promise(resolve=>{release=resolve}); ctx.ui.notify("start-complete","info"); });
 pi.on("ui_prompt_end",(_,ctx)=>{ if (!release) throw new Error("end overtook start body"); ctx.ui.notify("end-entered","info"); release(); });
}`)
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{config})
	if len(errs) > 0 || len(loaded) != 1 {
		t.Fatalf("LoadAll: %v", errs)
	}
	runner := inproc.NewRunner(loaded, t.TempDir())
	defer runner.Invalidate("test complete")
	runner.BeginUIPrompt(extension.UIPromptKindInput, "pending")()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	seen := make(map[string]bool)
	for range 2 {
		select {
		case record := <-records:
			seen[record] = true
		case <-ctx.Done():
			t.Fatal("prompt promise did not drain")
		}
	}
	if !seen["end-entered"] || !seen["start-complete"] {
		t.Fatalf("notifications: %v", seen)
	}
}
