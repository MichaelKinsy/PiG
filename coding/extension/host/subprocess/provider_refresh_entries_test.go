package subprocess

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func configRefreshCount(h *Host) int {
	n := 0
	h.configRefreshes.Range(func(_, _ any) bool { n++; return true })
	return n
}

// The host's publication entry of a config-form refreshModels request (Host.configRefreshes) is gone after completion, error, cancellation and the extension's exit: nothing outlives the request.
func TestConfigRefreshEntryIsFreedWhenTheRequestSettles(t *testing.T) {
	var mu sync.Mutex
	var cfg extension.ProviderConfig
	started := make(chan string, 8)
	release := make(chan struct{})
	signalled := make(chan bool, 8)
	ext := sdk.New("probe")
	ext.RegisterProvider("dyn", sdk.ProviderConfig{"api": "dyn-api", "baseUrl": "https://dyn.test/v1", "apiKey": "k",
		"models": []any{map[string]any{"id": "a", "name": "A", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100}},
		"refreshModels": sdk.ProviderRefreshModelsFunc(func(r sdk.RefreshModelsContext) ([]map[string]any, error) {
			mode := r.Credential.String("access")
			if _, err := r.Publish(sdk.ModelsPublication{Update: func() error { return nil }}); err != nil {
				return nil, err
			}
			switch mode {
			case "error":
				return nil, errors.New("probe error")
			case "cancel":
				started <- mode
				select {
				case <-r.Signal.Done():
					signalled <- true
				case <-release:
					signalled <- false
				}
				return nil, r.Signal.Err()
			case "exit":
				started <- mode
				<-release
			}
			return nil, nil
		})})
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetUIBridge(NewUIBridge(func() {}))
	host.SetProviderCallbacks(func(name string, c extension.ProviderConfig) error {
		mu.Lock()
		cfg = c
		mu.Unlock()
		return nil
	}, func(string) {})
	connCh := make(chan net.Conn, 1)
	if _, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "probe", Path: "/ext/probe.go", Enabled: true}, func(conn net.Conn) error { connCh <- conn; return ext.RunWithConn(conn) }); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	mu.Lock()
	refresh := cfg.RefreshModels
	mu.Unlock()
	if refresh == nil {
		t.Fatal("no RefreshModels on the registered config")
	}
	run := func(ctx context.Context, mode string) error {
		_, err := refresh(ai.RefreshModelsContext{Credential: &ai.Credential{Type: ai.CredentialOAuth, Access: mode}, Signal: ctx, Publish: func(ai.ModelsPublication) (bool, error) { return true, nil }})
		return err
	}
	if err := run(t.Context(), "ok"); err != nil || configRefreshCount(host) != 0 {
		t.Fatalf("completion: err=%v entries=%d", err, configRefreshCount(host))
	}
	if err := run(t.Context(), "error"); err == nil || configRefreshCount(host) != 0 {
		t.Fatalf("error: err=%v entries=%d", err, configRefreshCount(host))
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, "cancel") }()
	<-started
	if n := configRefreshCount(host); n != 1 {
		t.Fatalf("in flight: entries=%d, want 1", n)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel: no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel: refresh did not return")
	}
	select {
	case saw := <-signalled:
		if !saw {
			t.Fatal("cancel: the extension callback never saw its Signal cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel: extension callback never observed cancel")
	}
	if n := configRefreshCount(host); n != 0 {
		t.Fatalf("cancel: entries=%d", n)
	}
	go func() { done <- run(t.Context(), "exit") }()
	<-started
	if n := configRefreshCount(host); n != 1 {
		t.Fatalf("exit in flight: entries=%d, want 1", n)
	}
	(<-connCh).Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("exit: no error")
		}
		t.Logf("exit error: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("exit: refresh did not return after the extension's connection closed")
	}
	if n := configRefreshCount(host); n != 0 {
		t.Fatalf("exit: entries=%d", n)
	}
	close(release)
}
