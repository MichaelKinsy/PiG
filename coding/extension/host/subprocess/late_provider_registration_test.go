package subprocess

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A registerProvider call after the factory finished carries the declaration the register payload's ProviderDecl does, so the provider keeps the images, classifiers and streamSimple the extension implements. Pi applies pi.registerProvider's whole ProviderConfig at once once the runner is bound (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803,1875-1903, loader.ts:449-457, runner.ts:517-523).
func TestLateProviderRegistrationCallWiresDeclaredOperations(t *testing.T) {
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	var seen []extension.ProviderConfig
	host.SetProviderCallbacks(func(_ string, config extension.ProviderConfig) error {
		seen = append(seen, config)
		return nil
	}, nil)
	// The unstarted Conn records frames only; it owns no socket or workers to close.
	owner := withConn(&managedExt{}, NewConn("owner", nil))
	call := func(args string) {
		t.Helper()
		if _, err := host.handleProviderRegistrationCall(t.Context(), owner, owner.connection(), "", &CallPayload{Method: "registerProvider", Args: json.RawMessage(args)}); err != nil {
			t.Fatal(err)
		}
	}

	call(`{"name":"late","config":{"api":"late-chat-api","baseUrl":"https://late.test/v1","apiKey":"k"},"stream_simple":true,"image_apis":["late-images","other-images"],"classifier_apis":["late-classifier"]}`)
	declared := seen[0]
	if declared.StreamSimple == nil {
		t.Fatal("a declared streamSimple was dropped")
	}
	if got := slices.Sorted(func(yield func(ai.ImageAPI) bool) {
		for api := range declared.Images {
			if !yield(api) {
				return
			}
		}
	}); !reflect.DeepEqual(got, []ai.ImageAPI{"late-images", "other-images"}) {
		t.Fatalf("images = %v", got)
	}
	if classifier := declared.Classifiers["late-classifier"]; classifier == nil || classifier.Classify == nil || len(declared.Classifiers) != 1 {
		t.Fatalf("classifiers = %+v", declared.Classifiers)
	}
	if image := declared.Images["late-images"]; image == nil || image.GenerateImages == nil {
		t.Fatalf("images = %+v", declared.Images)
	}

	// A registration that declares nothing wires nothing: the registry keeps what an earlier one defined (model-runtime.ts:753-766).
	call(`{"name":"late","config":{"baseUrl":"https://late.test/v2"}}`)
	if again := seen[1]; again.StreamSimple != nil || again.Images != nil || again.Classifiers != nil {
		t.Fatalf("a registration without operations wired %+v", again)
	}
}
