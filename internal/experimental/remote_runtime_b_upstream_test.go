package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

func TestExperimentalDurableServerCompositionRemoteB(t *testing.T) {
	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:459
	t.Run("composes management attachment with Session service hydration", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		_, runtime := makeExperimentalServer(t)
		clientRuntime, err := OpenClientRuntime(t.Context(), ClientCommand{
			Command: "client",
			Connect: &TransportAddress{Transport: "unix", Path: runtime.SocketPath},
		}, OpenClientRuntimeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := clientRuntime.Dispose(); err != nil {
				t.Error(err)
			}
		})
		server, err := ActivateBuiltinClientServices(t.Context(), clientRuntime.Servers[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := server.Plugins.PrepareSession(t.Context(), services.PrepareSessionPluginsRequest{SessionId: "demo-1", PackagePaths: nil}); err != nil {
			t.Fatal(err)
		}
		if err := server.Management.Attach(t.Context(), "demo-1"); err != nil {
			t.Fatal(err)
		}
		if got := server.Session.Attachment().Value(); got == nil || *got != (services.SessionAttachmentState{Status: "attached", SessionID: "demo-1"}) {
			t.Fatalf("attachment = %#v, want attached/demo-1", got)
		}
		state := server.Models.State().Value()
		wantModel := &services.ModelRef{Provider: "anthropic", ModelId: "claude-sonnet-4-5"}
		if state == nil || !reflect.DeepEqual(state.Configuration.Model, wantModel) {
			t.Fatalf("Models state = %#v, want model %#v", state, wantModel)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:480
	t.Run("fences superseded attachment hydration by attachment generation", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		_, runtime := makeExperimentalServer(t)
		factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: runtime.SocketPath})
		if err != nil {
			t.Fatal(err)
		}
		connected, err := client.Connect(t.Context(), client.ClientOptions{ServerId: runtime.ServerId, TransportFactory: factory})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalClient(t, connected)
		var mu sync.Mutex
		var failures []error
		transportClient, releaseDelay := delaySessionServiceSubscription(t, connected, "demo-2", services.ModelsDefinition.Id())
		defer releaseDelay()
		binding := createSessionServiceBinding(t, connected, []string{services.ModelsDefinition.Id()}, ClientServiceSourceOptions{
			TransportClient: transportClient,
			OnError: func(err error) {
				mu.Lock()
				defer mu.Unlock()
				failures = append(failures, err)
			},
		})
		models, err := chord.UseRemoteClient(binding, services.ModelsDefinition)
		if err != nil {
			t.Fatal(err)
		}
		if err := binding.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		var states []services.SessionAttachmentState
		removeStateListener, err := binding.Attachment().Subscribe(func(state *services.SessionAttachmentState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
			mu.Lock()
			defer mu.Unlock()
			if state == nil {
				failures = append(failures, errors.New("Session source published a nil attachment state"))
				return
			}
			states = append(states, *state)
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(removeStateListener)

		attachSession(t, connected, "demo-2")
		if got := binding.Attachment().Value(); got == nil || *got != (services.SessionAttachmentState{Status: "attaching", SessionID: "demo-2"}) {
			t.Fatalf("attachment = %#v, want attaching/demo-2", got)
		}
		attachSession(t, connected, "demo-1")
		if err := binding.WhenAttached(t.Context(), "demo-1"); err != nil {
			t.Fatal(err)
		}
		if got := binding.Attachment().Value(); got == nil || *got != (services.SessionAttachmentState{Status: "attached", SessionID: "demo-1"}) {
			t.Fatalf("attachment = %#v, want attached/demo-1", got)
		}
		state := models.State().Value()
		wantModel := &services.ModelRef{Provider: "anthropic", ModelId: "claude-sonnet-4-5"}
		if state == nil || !reflect.DeepEqual(state.Configuration.Model, wantModel) {
			t.Fatalf("Models state = %#v, want model %#v", state, wantModel)
		}
		releaseDelay()
		removeStateListener()
		if err := binding.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		latestAttach := -1
		for i, state := range states {
			if state == (services.SessionAttachmentState{Status: "attaching", SessionID: "demo-1"}) {
				latestAttach = i
			}
		}
		if latestAttach < 0 {
			t.Fatalf("missing attaching/demo-1 transition: %#v", states)
		}
		for _, state := range states[latestAttach:] {
			if state == (services.SessionAttachmentState{Status: "attached", SessionID: "demo-2"}) || state == (services.SessionAttachmentState{Status: "degraded", SessionID: "demo-2"}) {
				t.Fatalf("superseded generation published after demo-1 attaching: %#v", states)
			}
		}
		if len(failures) != 0 {
			t.Fatalf("service errors = %v, want none", failures)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:536
	t.Run("observes keyed service instances and fences replacement generations over framed transport", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		installFauxSessionWorker(t)
		_, runtime := makeExperimentalServer(t)
		connected := attachExperimentalClient(t, runtime, "demo-1")
		var mu sync.Mutex
		var failures []error
		source, err := NewClientSessionServiceSource(connected, ClientServiceSourceOptions{OnError: func(err error) {
			mu.Lock()
			defer mu.Unlock()
			failures = append(failures, err)
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := source.Dispose(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		})
		catalogue, err := source.Catalogue(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(catalogue, chord.ServiceCatalogueEntry{ServiceId: keyedProbeDefinition.Id(), Mode: chord.ServiceKeyed}) {
			t.Fatalf("catalogue = %#v, want keyed test.keyed-probe", catalogue)
		}
		type observation struct {
			service keyedProbe
			value   *string
		}
		var observed []observation
		observations := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		consumer := chord.DefineFacet(chord.Facet{Id: "@test/keyed-probe-consumer", Setup: func(env *chord.FacetEnvironment) error {
			return chord.ObserveService(env, keyedProbeDefinition, func(_ context.Context, service keyedProbe) error {
				item := observation{service: service}
				if state := service.State().Value(); state != nil {
					item.value = new(state.Value)
				}
				mu.Lock()
				defer mu.Unlock()
				observed = append(observed, item)
				if len(observed) <= len(observations) {
					close(observations[len(observed)-1])
				}
				return nil
			})
		}})
		host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{
			Facets: []chord.Facet{consumer}, ServiceSources: []chord.RemoteServiceSource{source},
		})
		if err != nil {
			t.Fatal(err)
		}
		hostDisposed := false
		t.Cleanup(func() {
			if !hostDisposed {
				if err := host.Dispose(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			}
		})
		if got := source.Attachment().Value(); got == nil || *got != (services.SessionAttachmentState{Status: "attached", SessionID: "demo-1"}) {
			t.Fatalf("attachment = %#v, want attached/demo-1", got)
		}
		<-observations[0]
		mu.Lock()
		first := slices.Clone(observed)
		mu.Unlock()
		if len(first) != 1 || first[0].value == nil || *first[0].value != "first" {
			t.Fatalf("initial observations = %#v, want one first", first)
		}
		staleReplace := first[0].service.Replace
		if err := staleReplace(t.Context(), "second"); err != nil {
			t.Fatal(err)
		}
		<-observations[1]
		mu.Lock()
		second := slices.Clone(observed)
		mu.Unlock()
		if len(second) != 2 || second[1].value == nil || *second[1].value != "second" {
			t.Fatalf("replacement observations = %#v, want second generation second", second)
		}
		if err := staleReplace(t.Context(), "late"); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("stale replace = %v, want observation is closed", err)
		}
		replacedReplace := second[1].service.Replace
		attachSession(t, connected, "demo-2")
		if err := source.WhenAttached(t.Context(), "demo-2"); err != nil {
			t.Fatal(err)
		}
		// instances.ts:#start runs the handler during snapshot delivery, so the third observation exists as soon as whenAttached resolves (experimental-remote-runtime.test.ts:582).
		mu.Lock()
		third := slices.Clone(observed)
		mu.Unlock()
		if len(third) != 3 || third[2].value == nil || *third[2].value != "first" {
			t.Fatalf("reattachment observations = %#v, want third generation first", third)
		}
		if got := source.Attachment().Value(); got == nil || *got != (services.SessionAttachmentState{Status: "attached", SessionID: "demo-2"}) {
			t.Fatalf("attachment = %#v, want attached/demo-2", got)
		}
		if err := replacedReplace(t.Context(), "late"); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("replaced replace = %v, want observation is closed", err)
		}
		mu.Lock()
		capturedFailures := slices.Clone(failures)
		mu.Unlock()
		if len(capturedFailures) != 0 {
			t.Fatalf("service errors = %v, want none", capturedFailures)
		}
		if err := host.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
		hostDisposed = true
		if err := source.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:592
	t.Run("streams prompt events through the worker-owned service provider", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		installFauxSessionWorker(t)
		directory, _ := makeExperimentalServer(t)
		var mu sync.Mutex
		var eventTypes []string
		result, err := RunClient(t.Context(), ClientCommand{Command: "client", SessionId: new("demo-1"), Prompt: new("question")}, RunClientOptions{
			Directory: &directory,
			OnEvent: func(_ context.Context, raw json.RawMessage) error {
				var event struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(raw, &event); err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				eventTypes = append(eventTypes, event.Type)
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		prompted, ok := result.(ClientPromptedResult)
		if !ok || result.Kind() != "prompted" || prompted.Text != "deterministic remote answer" {
			t.Fatalf("result = %#v, want prompted/deterministic remote answer", result)
		}
		mu.Lock()
		defer mu.Unlock()
		for _, want := range []string{"run_start", "message_start", "message_update", "message_end", "entry_added", "run_end"} {
			if !slices.Contains(eventTypes, want) {
				t.Errorf("events = %v, missing %q", eventTypes, want)
			}
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:629
	t.Run("replicates terminal operation state after consecutive prompts", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		installFauxSessionWorker(t)
		_, runtime := makeExperimentalServer(t)
		connected := attachExperimentalClient(t, runtime, "demo-1")
		binding := createSessionServiceBinding(t, connected, []string{
			services.AgentControllerID, services.SessionPluginsDefinition.Id(), services.TranscriptDefinition.Id(),
		}, ClientServiceSourceOptions{})
		controller, err := chord.UseRemoteClient(binding, services.AgentControllerDefinition)
		if err != nil {
			t.Fatal(err)
		}
		transcript, err := chord.UseRemoteClient(binding, services.TranscriptDefinition)
		if err != nil {
			t.Fatal(err)
		}
		if err := binding.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		plugins, err := chord.UseRemoteClient(binding, services.SessionPluginsDefinition)
		if err != nil {
			t.Fatal(err)
		}
		if err := plugins.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, message := range []string{"first question", "second question"} {
			response, err := controller.Prompt(t.Context(), services.AgentPromptRequest{Message: message, Images: nil})
			if err != nil {
				t.Fatal(err)
			}
			if !response.Accepted || response.OperationID == nil {
				t.Fatalf("prompt %q response = %#v, want accepted with string operationId", message, response)
			}
			terminal := make(chan struct{})
			var once sync.Once
			remove, err := transcript.State().Subscribe(func(state *services.TranscriptState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
				if state != nil && state.Snapshot != nil && state.Snapshot.Operation == nil && state.Snapshot.LastResult != nil && state.Snapshot.LastResult.OperationID == *response.OperationID {
					once.Do(func() { close(terminal) })
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(remove)
			<-terminal
			remove()
		}
		if err := binding.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:663
	t.Run("stops an idle Session worker after its client disconnects", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		_, runtime := makeExperimentalServer(t)
		connected := attachExperimentalClient(t, runtime, "demo-1")
		pid, exists := runtime.WorkerPids()["demo-1"]
		if !exists {
			t.Fatal("demo-1 has no numeric worker PID")
		}
		if err := connected.Dispose(); err != nil {
			t.Fatal(err)
		}
		waitExperimentalWorkerRetired(t, runtime, "demo-1")
		// The manager drops the PID when the control socket closes, before the child is reaped and while kill(pid, 0) still succeeds for the exiting process. Upstream's poll (experimental-remote-runtime.test.ts:670-671) has the same window; join the native exit authority before asserting the process is gone.
		waitProcessExited(t, pid)
		if processExists(t, pid) {
			t.Fatalf("worker %d still exists after idle retirement", pid)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:675
	t.Run("starts one process per attached session and stops them during shutdown", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		_, runtime := makeExperimentalServer(t)
		attachExperimentalClients(t, runtime, []string{"demo-1", "demo-2"})
		pids := runtime.WorkerPids()
		if len(pids) != 2 {
			t.Fatalf("worker PIDs = %v, want two", pids)
		}
		unique := make(map[int]bool)
		for _, pid := range pids {
			unique[pid] = true
			if !processExists(t, pid) {
				t.Fatalf("attached worker %d does not exist", pid)
			}
		}
		if len(unique) != 2 {
			t.Fatalf("worker PIDs = %v, want two distinct processes", pids)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		if got := runtime.WorkerPids(); len(got) != 0 {
			t.Fatalf("closed server retains worker PIDs %v", got)
		}
		for pid := range unique {
			waitProcessExited(t, pid)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:689
	t.Run("server runtime replaces an exited worker on the next attach", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		directory := socketDir(t)
		runtime, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, runtime)
		connected := attachExperimentalClient(t, runtime, "demo-1")
		firstPID, exists := runtime.WorkerPids()["demo-1"]
		if !exists {
			t.Fatal("demo-1 has no numeric worker PID")
		}
		killWorkerProcess(t, firstPID)
		waitExperimentalWorkerRetired(t, runtime, "demo-1")
		attachSession(t, connected, "demo-1")
		replacementPID, exists := runtime.WorkerPids()["demo-1"]
		if !exists || replacementPID == firstPID {
			t.Fatalf("replacement PID = %d (present %v), want numeric PID unequal to %d", replacementPID, exists, firstPID)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:706
	t.Run("discovers workers after replacing the server", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		firstDirectory := socketDir(t)
		first, err := StartServer(t.Context(), StartServerOptions{Directory: &firstDirectory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, first)
		attachExperimentalClient(t, first, "demo-1")
		firstWorkerPID, exists := first.WorkerPids()["demo-1"]
		if !exists {
			t.Fatal("demo-1 has no numeric worker PID")
		}
		replacement, err := StartServer(t.Context(), StartServerOptions{Directory: &firstDirectory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, replacement)
		<-first.Closed()
		if err := first.ClosedError(); err != nil {
			t.Fatal(err)
		}
		if replacement.ServerId != first.ServerId {
			t.Fatalf("replacement server ID = %q, want %q", replacement.ServerId, first.ServerId)
		}
		if pid, exists := replacement.WorkerPids()["demo-1"]; !exists || pid != firstWorkerPID {
			t.Fatalf("adopted PID = %d (present %v), want %d", pid, exists, firstWorkerPID)
		}
		waitExperimentalWorkerRetired(t, first, "demo-1")
		if got := first.WorkerPids(); len(got) != 0 {
			t.Fatalf("replaced server retains worker PIDs %v", got)
		}
		if !processExists(t, firstWorkerPID) {
			t.Fatalf("adopted worker %d no longer exists", firstWorkerPID)
		}
		result, err := RunClient(t.Context(), ClientCommand{Command: "client"}, RunClientOptions{Directory: &firstDirectory})
		if err != nil {
			t.Fatal(err)
		}
		wantSessions := []services.SessionAddress{{ServerId: first.ServerId, SessionId: "demo-1"}, {ServerId: first.ServerId, SessionId: "demo-2"}}
		listed, ok := result.(ClientListResult)
		if !ok || result.Kind() != "list" || !reflect.DeepEqual(listed.Sessions, wantSessions) {
			t.Fatalf("result = %#v, want list %#v", result, wantSessions)
		}
		attachExperimentalClient(t, replacement, "demo-1")
		if pid, exists := replacement.WorkerPids()["demo-1"]; !exists || pid != firstWorkerPID {
			t.Fatalf("reattached PID = %d (present %v), want %d", pid, exists, firstWorkerPID)
		}
		attachExperimentalClient(t, replacement, "demo-2")
		if pid, exists := replacement.WorkerPids()["demo-2"]; !exists || pid == firstWorkerPID {
			t.Fatalf("demo-2 PID = %d (present %v), want numeric PID unequal to %d", pid, exists, firstWorkerPID)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:738
	t.Run("retires an unclaimed idle worker after replacement demand expires", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		directory := socketDir(t)
		t.Setenv("__PI_SESSION_WORKER_ORPHAN_DEMAND_GRACE_MS", "50")
		first, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, first)
		attachExperimentalClient(t, first, "demo-1")
		workerPID, exists := first.WorkerPids()["demo-1"]
		if !exists {
			t.Fatal("demo-1 has no numeric worker PID")
		}
		replacement, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, replacement)
		<-first.Closed()
		if err := first.ClosedError(); err != nil {
			t.Fatal(err)
		}
		if pid, exists := replacement.WorkerPids()["demo-1"]; !exists || pid != workerPID {
			t.Fatalf("adopted PID = %d (present %v), want %d", pid, exists, workerPID)
		}
		waitExperimentalWorkerRetired(t, replacement, "demo-1")
		// The manager drops a worker when its control socket closes, which precedes the kernel exit and the parent's reap. Join the reap before the existence check.
		waitProcessExited(t, workerPID)
		if processExists(t, workerPID) {
			t.Fatalf("unclaimed worker %d still exists after demand expiry", workerPID)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:757
	t.Run("restores tracked sessions that are outside the replacement catalog", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		directory := socketDir(t)
		emptySessionDir := t.TempDir()
		first, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, first)
		attachExperimentalClient(t, first, "demo-1")
		workerPID, hadWorker := first.WorkerPids()["demo-1"]
		replacement, err := StartServer(t.Context(), StartServerOptions{
			Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5"), SessionDir: &emptySessionDir,
		})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, replacement)
		<-first.Closed()
		if err := first.ClosedError(); err != nil {
			t.Fatal(err)
		}
		result, err := RunClient(t.Context(), ClientCommand{Command: "client"}, RunClientOptions{Directory: &directory})
		if err != nil {
			t.Fatal(err)
		}
		want := ClientListResult{Sessions: []services.SessionAddress{{ServerId: first.ServerId, SessionId: "demo-1"}}}
		if !reflect.DeepEqual(result, want) || result.Kind() != "list" {
			t.Fatalf("result = %#v, want %#v", result, want)
		}
		attachExperimentalClient(t, replacement, "demo-1")
		if pid, hasWorker := replacement.WorkerPids()["demo-1"]; hasWorker != hadWorker || pid != workerPID {
			t.Fatalf("reattached PID = %d (present %v), want %d (present %v)", pid, hasWorker, workerPID, hadWorker)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:783
	t.Run("reports missing and ambiguous session selections", func(t *testing.T) {
		setupExperimentalRemoteTest(t)
		sharedDirectory := socketDir(t)
		for _, serverID := range []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"} {
			runtime, err := StartServer(t.Context(), StartServerOptions{
				Directory: &sharedDirectory, ServerId: &serverID, Provider: new("anthropic"), Model: new("claude-sonnet-4-5"),
			})
			if err != nil {
				t.Fatal(err)
			}
			trackExperimentalServer(t, runtime)
		}
		_, err := RunClient(t.Context(), ClientCommand{Command: "client", SessionId: new("missing")}, RunClientOptions{Directory: &sharedDirectory})
		if err == nil || !strings.Contains(err.Error(), "No discovered server contains session missing") {
			t.Fatalf("missing selection = %v, want No discovered server contains session missing", err)
		}
		_, err = RunClient(t.Context(), ClientCommand{Command: "client", SessionId: new("demo-1")}, RunClientOptions{Directory: &sharedDirectory})
		if err == nil || !strings.Contains(err.Error(), "Session demo-1 is available from more than one server") {
			t.Fatalf("ambiguous selection = %v, want Session demo-1 is available from more than one server", err)
		}
	})

	// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:806
	t.Run("rejects a duplicate session ID within one durable repository", func(t *testing.T) {
		agentDir := setupExperimentalRemoteTest(t)
		createExperimentalSessions(t, filepath.Join(agentDir, "experimental", "sessions"), []string{"demo-1"}, filepath.Join(agentDir, "other-cwd"))
		directory, _ := makeExperimentalServer(t)
		_, err := RunClient(t.Context(), ClientCommand{Command: "client", SessionId: new("demo-1")}, RunClientOptions{Directory: &directory})
		serverError, ok := err.(*client.ServerError) //nolint:errorlint // Upstream matches the returned error's own code, not a wrapped cause.
		if !ok || serverError == nil || serverError.Code != "session_ambiguous" {
			t.Fatalf("duplicate Session selection = %v, want code session_ambiguous", err)
		}
	})
}
