package coding

// pi: packages/coding-agent/src/core/resource-loader.ts

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type trustSequence struct {
	mu     sync.Mutex
	events []string
}

func (s *trustSequence) add(e string) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *trustSequence) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func trustedName(trusted bool) string {
	if trusted {
		return "trusted"
	}
	return "untrusted"
}

// sequencedLoader is a loader whose extension load hook records the trust state each pass sees.
func sequencedLoader(t *testing.T, seq *trustSequence, extra func(ExtensionLoadRequest) error) *DefaultResourceLoader {
	t.Helper()
	var loader *DefaultResourceLoader
	hook := func(_ context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error) {
		name := "final"
		if request.Bootstrap {
			name = "bootstrap"
		}
		seq.add(name + ":" + trustedName(loader.settings.IsProjectTrusted()))
		if extra != nil {
			if err := extra(request); err != nil {
				return LoadExtensionsResult{}, err
			}
		}
		return LoadExtensionsResult{Extensions: []extension.Extension{{Path: name}}}, nil
	}
	loader, _ = extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{LoadExtensions: hook})
	return loader
}

// resource-loader.ts:533-560: the trust callback runs after the bootstrap load, sees the project forced untrusted and the bootstrap result, and its
// answer is applied to the settings before the final load.
func TestDefaultResourceLoaderTrustCallbackRunsBetweenBootstrapAndFinalLoad(t *testing.T) {
	for _, answer := range []bool{true, false} {
		seq := &trustSequence{}
		loader := sequencedLoader(t, seq, nil)
		err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(_ context.Context, input ResolveProjectTrustInput) (bool, error) {
			seq.add("callback:" + trustedName(loader.settings.IsProjectTrusted()))
			if len(input.ExtensionsResult.Extensions) != 1 || input.ExtensionsResult.Extensions[0].Path != "bootstrap" {
				t.Errorf("callback got %+v, want the bootstrap result", input.ExtensionsResult)
			}
			return answer, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"bootstrap:untrusted", "callback:untrusted", "final:" + trustedName(answer)}
		if got := seq.snapshot(); !slices.Equal(got, want) {
			t.Fatalf("answer %v: sequence = %q, want %q", answer, got, want)
		}
		if loader.settings.IsProjectTrusted() != answer {
			t.Fatalf("answer %v: settings trusted = %v after the reload", answer, loader.settings.IsProjectTrusted())
		}
	}
}

// resource-loader.ts:541-545: a rejected bootstrap load or callback rejects reload() before the final load; the results of the previous reload stay and the context
// the caller passed reaches the callback.
func TestDefaultResourceLoaderTrustFailuresRejectTheReloadAndKeepThePreviousResult(t *testing.T) {
	boom := errors.New("trust refused")
	bootFail := errors.New("bootstrap failed")
	for _, tc := range []struct {
		name         string
		failBoot     bool
		callbackErr  error
		wantCallback bool
		wantErr      error
	}{
		{"callback rejects", false, boom, true, boom},
		{"bootstrap rejects", true, nil, false, bootFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seq := &trustSequence{}
			failing := false
			loader := sequencedLoader(t, seq, func(r ExtensionLoadRequest) error {
				if failing && tc.failBoot && r.Bootstrap {
					return bootFail
				}
				return nil
			})
			if err := loader.Reload(); err != nil {
				t.Fatal(err)
			}
			before := loader.GetExtensions()
			if len(before.Extensions) != 1 || before.Extensions[0].Path != "final" {
				t.Fatalf("first reload extensions = %+v", before.Extensions)
			}
			failing = true
			type ctxKey struct{}
			ctx := context.WithValue(t.Context(), ctxKey{}, "caller")
			called := false
			err := loader.ReloadWith(ctx, ResourceLoaderReloadOptions{ResolveProjectTrust: func(cbCtx context.Context, _ ResolveProjectTrustInput) (bool, error) {
				called = true
				if cbCtx.Value(ctxKey{}) != "caller" {
					t.Error("the callback did not receive the reload's context")
				}
				return true, tc.callbackErr
			}})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("reload error = %v, want %v", err, tc.wantErr)
			}
			if called != tc.wantCallback {
				t.Fatalf("callback called = %v, want %v", called, tc.wantCallback)
			}
			after := loader.GetExtensions()
			if len(after.Extensions) != 1 || after.Extensions[0].Path != "final" {
				t.Fatalf("extensions after the failed reload = %+v, want the previous result", after.Extensions)
			}
			for _, e := range seq.snapshot()[1:] {
				if e == "final:trusted" || e == "final:untrusted" {
					t.Fatalf("a final load ran after the failure: %q", seq.snapshot())
				}
			}
			if loader.settings.IsProjectTrusted() {
				t.Fatal("the project is trusted after a failed decision")
			}
		})
	}
}

// Go serialises Reload calls (opMu): a reload holds the loader's operation lock while its extension load runs, so another Reload or ExtendResources
// cannot start its passes in between.
func TestDefaultResourceLoaderReloadHoldsTheOperationLockDuringTheExtensionLoad(t *testing.T) {
	seq := &trustSequence{}
	var held bool
	var loader *DefaultResourceLoader
	loader = sequencedLoader(t, seq, func(r ExtensionLoadRequest) error {
		if !r.Bootstrap {
			if loader.opMu.TryLock() {
				loader.opMu.Unlock()
				return errors.New("the operation lock was free during the final extension load")
			}
			held = true
		}
		return nil
	})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("the final extension load never ran")
	}
	if !loader.opMu.TryLock() {
		t.Fatal("the operation lock is still held after Reload returned")
	}
	loader.opMu.Unlock()
}
