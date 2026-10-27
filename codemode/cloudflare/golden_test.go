package cloudflare_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/codemode/cloudflare"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// testdata/pi-golden.json is what Pi's packages/codemode/src/cloudflare.ts does at the pin, recorded by testdata/oracle.mjs
// (cloudflare.ts itself, with the two Worker-only imports supplied by testdata/hooks.mjs): the limits of each object, the frames
// an object's execute sends for a request that makes no tool call, and the results of createInlineCodemodeSandbox. Regenerate it
// with `PI_UPSTREAM=.upstream/current node codemode/cloudflare/testdata/oracle.mjs > codemode/cloudflare/testdata/pi-golden.json`.

type piGolden struct {
	Pin       string `json:"pin"`
	Constants struct {
		DefaultRemoteMemoryLimitBytes uint32 `json:"defaultRemoteMemoryLimitBytes"`
	} `json:"constants"`
	QuickJSWasmIsAModule bool `json:"quickjsWasmIsAModule"`
	Objects              map[string]struct {
		MaxInterruptBudget  int    `json:"maxInterruptBudget"`
		MaxMemoryLimitBytes uint32 `json:"maxMemoryLimitBytes"`
	} `json:"objects"`
	Executes []struct {
		Name    string                 `json:"name"`
		Object  string                 `json:"object"`
		Request codemode.RemoteRequest `json:"request"`
		Frames  []any                  `json:"frames"`
	} `json:"executes"`
	Inlines []struct {
		Name    string `json:"name"`
		Options struct {
			InterruptBudget  int      `json:"interruptBudget"`
			MemoryLimitBytes uint32   `json:"memoryLimitBytes"`
			Tools            []string `json:"tools"`
			Wasm             bool     `json:"wasm"`
		} `json:"options"`
		Code   string         `json:"code"`
		Result map[string]any `json:"result"`
	} `json:"inlines"`
}

func loadGolden(t *testing.T) piGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/pi-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden piGolden
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Pin != pigversion.UpstreamVersion {
		t.Fatalf("pi-golden.json was recorded at Pi %s, the pin is %s: regenerate it", golden.Pin, pigversion.UpstreamVersion)
	}
	return golden
}

// goldenObject is the object of the golden's name: Pi's test subclasses override the same two limits.
func goldenObject(name string, zero bool) *cloudflare.SandboxDurableObject {
	// The zero value means the defaults, as an object of Pi's class that overrides no field does.
	object := &cloudflare.SandboxDurableObject{}
	if !zero {
		object = cloudflare.NewSandboxDurableObject()
	}
	switch name {
	case "small":
		object.MaxInterruptBudget = 3
	case "medium":
		object.MaxInterruptBudget = 40
	case "tight":
		object.MaxMemoryLimitBytes = 2 << 20
	case "roomy":
		object.MaxMemoryLimitBytes = 64 << 20
	}
	return object
}

// preludeHash is the one part of a fingerprint that differs: PiG's prelude is Pi's with divergence D145 applied.
var preludeHash = regexp.MustCompile(`prelude [0-9a-f]{8}`)

func maskPreludeHash(value any) any {
	switch v := value.(type) {
	case string:
		return preludeHash.ReplaceAllString(v, "prelude <hash>")
	case []any:
		for i := range v {
			v[i] = maskPreludeHash(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = maskPreludeHash(v[k])
		}
	}
	return value
}

func decoded(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTheObjectsLimitsAreThoseOfPisCloudflareEntry(t *testing.T) {
	golden := loadGolden(t)
	if cloudflare.DefaultRemoteMemoryLimitBytes != golden.Constants.DefaultRemoteMemoryLimitBytes {
		t.Errorf("DefaultRemoteMemoryLimitBytes = %d, Pi %d", cloudflare.DefaultRemoteMemoryLimitBytes, golden.Constants.DefaultRemoteMemoryLimitBytes)
	}
	if !golden.QuickJSWasmIsAModule || len(cloudflare.QuickJSWasm()) == 0 {
		t.Errorf("QuickJSWasm is the bundled module: %d bytes, Pi's is a module: %v", len(cloudflare.QuickJSWasm()), golden.QuickJSWasmIsAModule)
	}
	for name, want := range golden.Objects {
		object := goldenObject(name, false)
		if object.MaxInterruptBudget != want.MaxInterruptBudget || object.MaxMemoryLimitBytes != want.MaxMemoryLimitBytes {
			t.Errorf("%s object limits = %d, %d; Pi %d, %d", name, object.MaxInterruptBudget, object.MaxMemoryLimitBytes, want.MaxInterruptBudget, want.MaxMemoryLimitBytes)
		}
	}
}

func TestTheObjectsExecuteSendsThePiFrames(t *testing.T) {
	golden := loadGolden(t)
	for _, c := range golden.Executes {
		for _, zero := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s (zero value %v)", c.Name, zero), func(t *testing.T) {
				var mu sync.Mutex
				frames := []any{}
				exchange := func(_ context.Context, messages []codemode.RemoteMessage, reply func(*codemode.RemoteReply, error)) {
					mu.Lock()
					frames = append(frames, decoded(t, messages))
					mu.Unlock()
					reply(nil, nil)
				}
				if err := goldenObject(c.Object, zero).Execute(t.Context(), c.Request, exchange); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				defer mu.Unlock()
				if got, want := maskPreludeHash(decoded(t, frames)), maskPreludeHash(decoded(t, c.Frames)); !reflect.DeepEqual(got, want) {
					gotJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want)
					t.Errorf("frames\nPiG %s\nPi  %s", gotJSON, wantJSON)
				}
			})
		}
	}
}

func TestCreateInlineSandboxResultsAreThoseOfPisCreateInlineCodemodeSandbox(t *testing.T) {
	golden := loadGolden(t)
	for _, c := range golden.Inlines {
		t.Run(c.Name, func(t *testing.T) {
			options := codemode.InlineSandboxOptions{SandboxOptions: codemode.SandboxOptions{MemoryLimitBytes: c.Options.MemoryLimitBytes}, InterruptBudget: c.Options.InterruptBudget}
			for _, name := range c.Options.Tools {
				options.Tools = append(options.Tools, codemode.Tool{Name: name, Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
					if args == nil {
						args = json.RawMessage("null")
					}
					return json.RawMessage(`{"echoed":` + string(args) + `}`), nil
				}})
			}
			if c.Options.Wasm {
				// Pi's createInlineCodemodeSandbox puts its own module after the options: a module of the caller is ignored.
				options.Wasm = func() ([]byte, error) { return []byte("not a module"), nil }
			}
			sandbox, err := cloudflare.CreateInlineSandbox(options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sandbox.Close() }()
			result, err := sandbox.Execute(t.Context(), c.Code, codemode.ExecuteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]any{"ok": result.OK, "output": []any{}, "calls": []any{}}
			if result.OK {
				if result.Value == nil {
					got["value"] = map[string]any{"undefined": true}
				} else {
					var value any
					_ = json.Unmarshal(result.Value, &value)
					got["value"] = value
				}
			} else {
				got["error"] = result.Error
			}
			for _, call := range result.Calls {
				got["calls"] = append(got["calls"].([]any), map[string]any{"name": call.Name, "status": string(call.Status)})
			}
			if g, w := decoded(t, got), decoded(t, c.Result); !reflect.DeepEqual(g, w) {
				gotJSON, _ := json.Marshal(g)
				wantJSON, _ := json.Marshal(w)
				t.Errorf("result\nPiG %s\nPi  %s", gotJSON, wantJSON)
			}
		})
	}
}
