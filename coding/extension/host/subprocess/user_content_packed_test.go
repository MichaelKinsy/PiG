package subprocess

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// Packing is supported for Go, Rust and Python factories; Node is isolated,
// and Go's fused realization is covered by TestUserMessageContentAcrossSDKs.
func TestPackedUserMessageContentAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	cases := []struct {
		name    string
		configs func(*testing.T) []ExtConfig
	}{
		{"go", func(t *testing.T) []ExtConfig {
			return []ExtConfig{packedFactoryConfig("user-go-a", writePackedFactoryModule(t, "example.com/user/goa", "user-go-a", "tool-a"), "example.com/user/goa", "a"), packedFactoryConfig("user-go-b", writePackedFactoryModule(t, "example.com/user/gob", "user-go-b", "tool-b"), "example.com/user/gob", "b")}
		}},
		{"python", func(t *testing.T) []ExtConfig {
			return []ExtConfig{packedPythonFactoryConfig("user-py-a", writePackedPythonFactoryModule(t, "user_py_a", "user-py-a", "tool-a"), "user_py_a", "a"), packedPythonFactoryConfig("user-py-b", writePackedPythonFactoryModule(t, "user_py_b", "user-py-b", "tool-b"), "user_py_b", "b")}
		}},
		{"rust", func(t *testing.T) []ExtConfig {
			return []ExtConfig{packedRustFactoryConfig("user-rs-a", writePackedRustFactoryCrate(t, "user_rs_a", "user-rs-a", "tool-a"), "user_rs_a", "a"), packedRustFactoryConfig("user-rs-b", writePackedRustFactoryCrate(t, "user_rs_b", "user-rs-b", "tool-b"), "user_rs_b", "b")}
		}},
	}
	const want = `[{"text":"first","type":"text"},{"data":"aW1hZ2U=","mimeType":"image/png","type":"image"},{"text":"second","type":"text"}]`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			defer host.Shutdown("test done")
			calls := make(chan string, 2)
			bridge := NewUIBridge(func() {})
			bridge.SetActions(&HostCallbacks{SendUserMessage: func(content any, opts SendUserMessageOptions) error {
				raw, err := json.Marshal(content)
				if err != nil {
					return err
				}
				calls <- string(raw) + ":" + opts.DeliverAs
				return nil
			}})
			host.SetUIBridge(bridge)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			configs := tc.configs(t)
			loaded, errs := host.LoadAll(ctx, configs)
			if len(errs) != 0 || len(loaded) != 2 {
				t.Fatalf("load: %v (%d)", errs, len(loaded))
			}
			first, second := host.exts[configs[0].Name], host.exts[configs[1].Name]
			if first.packedCellKey == "" || first.packedCellKey != second.packedCellKey {
				t.Fatal("fixtures did not share a packed cell")
			}
			for _, ext := range loaded {
				cmd, ok := ext.Commands["send_user_content"]
				if !ok {
					t.Fatal("command missing")
				}
				if err := cmd.Handler(ctx, ""); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-calls:
					if got != want+":steer" {
						t.Fatalf("content %s", got)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
	}
}
