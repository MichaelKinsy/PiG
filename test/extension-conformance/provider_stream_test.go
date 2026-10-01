package extensionconformance

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
)

func TestProviderProducersAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, source string
		fused        bool
		packed       bool
	}{
		{"go-isolated", "testdata/provider-go", false, false}, {"go-packed", "testdata/provider-go", false, true},
		{"python-isolated", "testdata/provider-python", false, false}, {"python-packed", "testdata/provider-python", false, true},
		{"rust-isolated", "testdata/provider-rust", false, false}, {"rust-packed", "testdata/provider-rust", false, true},
		{"node-isolated", "../../coding/testdata/provider-stream-producer.mjs", false, false}, {"node-packed", "../../coding/testdata/provider-stream-producer.mjs", false, true},
		{"fused-go", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "primary", Provider: ai.NewFauxProvider(ai.FauxConfig{})}, NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			host := subprocess.NewHost(t.TempDir())
			defer host.Shutdown("test done")
			host.SetProviderCallbacks(services.Registry().RegisterProvider, services.Registry().UnregisterProvider)
			bridge := subprocess.NewUIBridge(func() {})
			entered := make(chan struct{}, 1)
			// The provider blocks before returning its stream. Use a UI-independent host call as its entry barrier; headless Notify is correctly a no-op.
			bridge.SetHostAction("exec", func(_ context.Context, command string, _ []string, _ *extension.ExecOptions) (extension.ExecResult, error) {
				if command != "provider-started" {
					return extension.ExecResult{}, fmt.Errorf("unexpected provider fixture command %q", command)
				}
				entered <- struct{}{}
				return extension.ExecResult{}, nil
			})
			detach := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{CurrentModel: session.Model, ModelLookup: services.ModelRuntime().GetModel, ModelCatalog: services.ModelRuntime().GetModels, Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return coding.BuildModel(spec, services) }, SessionHandle: session})
			defer detach()
			host.SetUIBridge(bridge)
			if tc.fused {
				_, err = host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "provider-producer", Enabled: true}, func(conn net.Conn) error { return testfixture.ProviderProducer().RunWithConn(conn) })
			} else {
				name := "provider-producer"
				if strings.HasPrefix(tc.name, "node-") {
					name = "provider-stream-producer"
				}
				source, resolveErr := filepath.Abs(tc.source)
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				cfg, _, resolveErr := subprocess.ResolveExtConfigWithIdentity(source, name)
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				cfgs := []subprocess.ExtConfig{cfg}
				if tc.packed {
					siblingPath := tc.source + "-sibling"
					if strings.HasPrefix(tc.name, "node-") {
						siblingPath = "testdata/provider-sibling.mjs"
					}
					absoluteSibling, pathErr := filepath.Abs(siblingPath)
					if pathErr != nil {
						t.Fatal(pathErr)
					}
					sibling, _, siblingErr := subprocess.ResolveExtConfigWithIdentity(absoluteSibling, "provider-sibling")
					if siblingErr != nil {
						t.Fatal(siblingErr)
					}
					cfgs = append(cfgs, sibling)
				} else {
					cfgs[0].Isolation = "isolated"
				}
				_, loadErrors := host.LoadAll(t.Context(), cfgs)
				if len(loadErrors) > 0 {
					err = loadErrors[0]
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			// Pi model-runtime.ts:753-787 publishes a configured registration in the available snapshot before registerProvider returns; the selector, cycling and SDK snapshot readers need no reload.
			if !availableModel(services.ModelRuntime().GetAvailableSnapshot(), "extension-provider", "faux") || !availableModel(services.Registry().GetAvailableModelData(), "extension-provider", "faux") {
				t.Fatalf("registered provider missing from availability: snapshot=%v registry=%v", services.ModelRuntime().GetAvailableSnapshot(), services.Registry().GetAvailableModelData())
			}
			consumer, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "provider-stream-consumer", Source: filepath.Join("..", "..", "coding", "testdata", "provider-stream-consumer.mjs"), Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"stream", "streamSimple"} {
				if err := consumer.Commands["stream-custom"].Handler(t.Context(), method); err != nil {
					t.Fatalf("%s: %v", method, err)
				}
			}
			model := services.ModelRuntime().GetModel("extension-provider", "faux")
			failed := services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}, ai.StreamOptions{APIKey: "wrong-key"})
			if failed.StopReason != ai.StopReasonError || !strings.Contains(failed.ErrorMessage, "wrong key") {
				t.Fatalf("provider rejection=%+v", failed)
			}
			ctx, cancel := context.WithCancel(t.Context())
			pending := services.ModelRuntime().Stream(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("cancel")}}}, ai.StreamOptions{})
			if strings.HasPrefix(tc.name, "node-") {
				for range pending.Events(ctx) {
					break
				}
			} else {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					cancel()
					t.Fatalf("provider callback did not start: %+v", pending.Result())
				}
			}
			cancel()
			result := pending.Result()
			// api/lazy.ts:4-22 reports a throwing/cancelled setup or iterator as error; only an explicit provider aborted event has stopReason aborted.
			if result.StopReason != ai.StopReasonError || result.ErrorMessage == "" {
				t.Fatalf("cancel result=%+v", result)
			}
			other, createErr := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if createErr != nil {
				t.Fatal(createErr)
			}
			if other.ModelRuntime().GetModel("extension-provider", "faux") != nil {
				t.Fatal("provider leaked outside its owning registry")
			}
		})
	}
}

func availableModel(models []*ai.Model, providerID, modelID string) bool {
	for _, model := range models {
		if model.ProviderMeta.ProviderID == providerID && model.ID == modelID {
			return true
		}
	}
	return false
}
