//go:build !pig_strip_node_extensions

package subprocess

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type factoryProviderRegistration struct {
	Name   string                   `json:"name"`
	Config extension.ProviderConfig `json:"config"`
}

type factoryProviderRecording struct {
	mu         sync.Mutex
	registered []factoryProviderRegistration
	removed    []string
}

func (r *factoryProviderRecording) bind(host *Host) {
	host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.registered = append(r.registered, factoryProviderRegistration{Name: name, Config: config})
		return nil
	}, func(name string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.removed = append(r.removed, name)
	})
}

func (r *factoryProviderRecording) assertWorkingOnly(t *testing.T, host *Host) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	want := []factoryProviderRegistration{{Name: "working-provider", Config: extension.ProviderConfig{BaseURL: "https://provider.test/v1", APIKey: "provider-test-key"}}}
	if !reflect.DeepEqual(r.registered, want) || len(r.removed) != 0 {
		t.Fatalf("provider publication registered=%+v removed=%v; want only %+v", r.registered, r.removed, want)
	}
	if got := host.ProviderNames("working"); !slices.Equal(got, []string{"working-provider"}) {
		t.Fatalf("working provider ownership=%v", got)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8423-extension-factory-failure.test.ts:12
// Pi's per-factory journal becomes an uncommitted subprocess registration: only a successful factory can publish it to the shared Host.
func TestUpstreamFactoryFailureDiscardsRuntimeChangesAndDisablesFailedAPI(t *testing.T) {
	nodeCellRequireNode(t)
	root := filepath.Join(findModuleRoot(t), "test/parity/scenarios/extensions-runtime/testdata/factory-failure")
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	var providers factoryProviderRecording
	providers.bind(host)
	configs := []ExtConfig{
		{Name: "working", Source: filepath.Join(root, "working.mjs"), Enabled: true},
		{Name: "failing", Source: filepath.Join(root, "failing.mjs"), Enabled: true},
		{Name: "survivor", Source: filepath.Join(root, "survivor.mjs"), Enabled: true},
	}
	loaded, failures := host.LoadAll(t.Context(), configs)
	if len(loaded) != 2 || loaded[0].Name != "working" || loaded[1].Name != "survivor" || len(failures) != 1 {
		t.Fatalf("loaded=%v failures=%v", loaded, failures)
	}
	failure, ok := errors.AsType[*FactoryLoadError](failures[0])
	if !ok || failure.Message != "Failed to load extension: factory failed" {
		t.Fatalf("factory error=%v", failures[0])
	}
	if got := host.FlagDefault("", "failed-flag"); got != nil {
		t.Fatalf("failed default reached shared flags: %v", got)
	}
	if got := host.FlagDefault("", "timer-flag"); got != nil {
		t.Fatalf("failed timer default reached shared flags: %v", got)
	}
	providers.assertWorkingOnly(t, host)
	var report struct {
		FlagDuringLoad bool    `json:"flagDuringLoad"`
		EventCalls     int     `json:"eventCalls"`
		SurvivorEvents int     `json:"survivorEvents"`
		LateEventCalls int     `json:"lateEventCalls"`
		TimerError     *string `json:"timerError"`
		Results        []struct {
			Method       string  `json:"method"`
			Asynchronous bool    `json:"asynchronous"`
			Error        *string `json:"error"`
		} `json:"results"`
	}
	raw := loaded[1].Commands["factory-failure-report"].Description
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatal(err)
	}
	if !report.FlagDuringLoad || report.EventCalls != 0 || report.LateEventCalls != 0 || report.SurvivorEvents != 2 {
		t.Fatalf("factory state=%s", raw)
	}
	want := "Extension \"" + configs[1].Source + "\" failed to load and its API is no longer active."
	if report.TimerError == nil || *report.TimerError != want {
		t.Fatalf("failed factory timer was not invalidated: %s", raw)
	}
	var capturedFlag bool
	for _, result := range report.Results {
		if result.Method == "registerFlag" {
			capturedFlag = true
			if result.Asynchronous || result.Error == nil || *result.Error != want {
				t.Fatalf("captured API registerFlag: %s", raw)
			}
		}
	}
	if !capturedFlag {
		t.Fatal("captured API was not exercised")
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8423-extension-factory-failure.test.ts:57
// Two direct loads overlap at the factory barrier against one Host. Failure must not restore a global snapshot over the successful sibling's publication.
func TestUpstreamFactoryFailurePreservesConcurrentlyLoadedProvider(t *testing.T) {
	nodeCellRequireNode(t)
	ctx, cancel := context.WithCancel(t.Context())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	var providers factoryProviderRecording
	providers.bind(host)
	var workers sync.WaitGroup
	var gate net.Conn
	type accepted struct {
		conn net.Conn
		err  error
	}
	connection := make(chan accepted, 1)
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		if gate != nil {
			_ = gate.Close()
		}
		host.Shutdown("test done")
		workers.Wait()
		select {
		case result := <-connection:
			if result.conn != nil {
				_ = result.conn.Close()
			}
		default:
		}
	})
	root := t.TempDir()
	failing := filepath.Join(root, "failing.mjs")
	address := listener.Addr().(*net.TCPAddr)
	write(t, failing, fmt.Sprintf(`import { connect } from "node:net";
import { once } from "node:events";
export default async function(pi) {
 pi.registerProvider("failed-provider", {baseUrl:"https://provider.test/v1",apiKey:"provider-test-key"});
 const socket = connect(%d, "127.0.0.1");
 try {
  await once(socket, "connect");
  socket.write("ready\n");
  await once(socket, "data");
 } finally { socket.destroy(); }
 throw new Error("factory failed");
}`, address.Port))
	loadResult := make(chan error, 1)
	workers.Go(func() {
		_, err := host.Load(ctx, ExtConfig{Name: "failing", Source: failing, Enabled: true})
		loadResult <- err
	})
	workers.Go(func() {
		conn, err := listener.Accept()
		connection <- accepted{conn: conn, err: err}
	})
	select {
	case result := <-connection:
		if result.err != nil {
			t.Fatal(result.err)
		}
		gate = result.conn
	case err := <-loadResult:
		t.Fatalf("failing factory settled before barrier: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if line, err := bufio.NewReader(gate).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("factory barrier=%q error=%v", line, err)
	}
	working := filepath.Join(findModuleRoot(t), "test/parity/scenarios/extensions-runtime/testdata/factory-failure/working.mjs")
	if _, err := host.Load(ctx, ExtConfig{Name: "working", Source: working, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	providers.assertWorkingOnly(t, host)
	if _, err := gate.Write([]byte("release\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-loadResult:
		failure, ok := errors.AsType[*FactoryLoadError](err)
		if !ok || failure.Message != "Failed to load extension: factory failed" {
			t.Fatalf("factory failure=%v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	providers.assertWorkingOnly(t, host)
}
