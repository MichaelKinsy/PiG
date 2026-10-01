package extension

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
)

func TestProviderRuntimeBindingOrderAndErrors(t *testing.T) {
	runtime := CreateExtensionRuntime()
	for _, name := range []string{"first", "broken", "last"} {
		if err := runtime.RegisterProvider(name, ProviderConfig{}, name+".ts"); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	sentinel := errors.New("invalid")
	runtime.BindProviderActions(ProviderActions{RegisterProvider: func(name string, _ ProviderConfig) error {
		calls = append(calls, "register:"+name)
		// Both reads and registration can re-enter while a callback runs.
		if name == "first" {
			if len(runtime.PendingProviderRegistrations()) == 0 {
				t.Error("queue cleared before its bind iteration completed")
			}
			return runtime.RegisterProvider("nested", ProviderConfig{})
		}
		if name == "broken" {
			return sentinel
		}
		return nil
	}}, func(err *ExtensionError) {
		if err.ExtensionPath != "broken.ts" || err.Event != "register_provider" || err.Error != "invalid" {
			t.Errorf("error=%+v", err)
		}
		calls = append(calls, "error")
	})
	want := []string{"register:first", "register:broken", "error", "register:last", "register:nested"}
	if !reflect.DeepEqual(calls, want) || len(runtime.PendingProviderRegistrations()) != 0 {
		t.Fatalf("calls=%v; want %v and a drained queue", calls, want)
	}
	if err := runtime.RegisterProvider("broken", ProviderConfig{}); !errors.Is(err, sentinel) {
		t.Fatalf("post-bind error=%v", err)
	}
	if len(runtime.PendingProviderRegistrations()) != 0 {
		t.Fatal("post-bind failure was queued")
	}
}

func TestProviderRuntimeUnregisterPreservesOtherQueuedProviders(t *testing.T) {
	runtime := CreateExtensionRuntime()
	for _, name := range []string{"keep-first", "remove", "keep-last", "remove"} {
		if err := runtime.RegisterProvider(name, ProviderConfig{}); err != nil {
			t.Fatal(err)
		}
	}
	runtime.UnregisterProvider("remove")
	runtime.UnregisterProvider("absent")
	var names []string
	for _, entry := range runtime.PendingProviderRegistrations() {
		names = append(names, entry.Name)
	}
	if !reflect.DeepEqual(names, []string{"keep-first", "keep-last"}) {
		t.Fatalf("remaining queue=%v", names)
	}
}

func TestProviderRuntimePostBindWaitsForCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime := CreateExtensionRuntime()
		entered, release := make(chan struct{}), make(chan struct{})
		sentinel := errors.New("registry rejected")
		runtime.BindProviderActions(ProviderActions{RegisterProvider: func(string, ProviderConfig) error {
			close(entered)
			<-release
			return sentinel
		}}, nil)
		result := make(chan error, 1)
		go func() { result <- runtime.RegisterProvider("provider", ProviderConfig{}) }()
		<-entered
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("registration returned before callback: %v", err)
		default:
		}
		close(release)
		if err := <-result; !errors.Is(err, sentinel) {
			t.Fatalf("registration error=%v", err)
		}
	})
}

func BenchmarkProviderRuntimeBind(b *testing.B) {
	for _, count := range []int{0, 1, 64, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				runtime := CreateExtensionRuntime()
				for range count {
					if err := runtime.RegisterProvider("provider", ProviderConfig{}); err != nil {
						b.Fatal(err)
					}
				}
				registered := 0
				runtime.BindProviderActions(ProviderActions{RegisterProvider: func(string, ProviderConfig) error { registered++; return nil }}, nil)
				if registered != count || len(runtime.PendingProviderRegistrations()) != 0 {
					b.Fatal("bind lost registrations")
				}
			}
		})
	}
}

// Upstream runner.ts:500-512 flushes pendingVirtualModelRegistrations with a for-of loop over the live array, so a registration a callback queues during the flush is applied in the same flush, like the provider queue above. A failure is reported with its extension path and the flush continues.
func TestVirtualModelRuntimeFlushAppliesRegistrationsQueuedDuringTheFlush(t *testing.T) {
	runtime := CreateExtensionRuntime()
	for _, id := range []string{"first", "broken", "last"} {
		if err := runtime.RegisterVirtualModel(VirtualModelDefinition{Provider: "router", ID: id}, id+".ts"); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	sentinel := errors.New("Virtual model router/broken conflicts with a physical model.")
	runtime.BindProviderActions(ProviderActions{
		RegisterVirtualModel: func(definition VirtualModelDefinition) error {
			calls = append(calls, "register:"+definition.ID)
			switch definition.ID {
			case "first":
				if len(runtime.PendingVirtualModelRegistrations()) == 0 {
					t.Error("queue cleared before its bind iteration completed")
				}
				return runtime.RegisterVirtualModel(VirtualModelDefinition{Provider: "router", ID: "nested"}, "nested.ts")
			case "broken":
				return sentinel
			}
			return nil
		},
		UnregisterVirtualModel: func(provider, id string) { calls = append(calls, "unregister:"+provider+"/"+id) },
	}, func(err *ExtensionError) {
		if err.ExtensionPath != "broken.ts" || err.Event != "register_virtual_model" || err.Error != sentinel.Error() {
			t.Errorf("error=%+v", err)
		}
		calls = append(calls, "error")
	})
	want := []string{"register:first", "register:broken", "error", "register:last", "register:nested"}
	if !reflect.DeepEqual(calls, want) || len(runtime.PendingVirtualModelRegistrations()) != 0 {
		t.Fatalf("calls=%v; want %v and a drained queue", calls, want)
	}
	runtime.UnregisterVirtualModel("router", "last")
	if got := calls[len(calls)-1]; got != "unregister:router/last" {
		t.Fatalf("post-bind unregister = %q", got)
	}
}
