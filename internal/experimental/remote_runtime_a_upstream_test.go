package experimental

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

func TestExperimentalDurableServerCompositionA(t *testing.T) {
	requirePOSIXServerDirectory(t)
	cases := []struct {
		name string
		run  func(*testing.T, string)
	}{
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:85.
		{
			name: "uses PI_SERVER_DIR and PI_SERVER_ID",
			run: func(t *testing.T, _ string) {
				directory := socketDir(t)
				serverId := "00000000-0000-4000-8000-000000000001"
				t.Setenv("PI_SERVER_DIR", directory)
				t.Setenv("PI_SERVER_ID", serverId)
				runtime, err := StartServer(t.Context(), StartServerOptions{})
				if err != nil {
					t.Fatal(err)
				}
				trackExperimentalServer(t, runtime)
				if runtime.ServerId != serverId || runtime.SocketPath != filepath.Join(directory, serverId+".sock") {
					t.Fatalf("server identity/path = %q/%q, want %q/%q", runtime.ServerId, runtime.SocketPath, serverId, filepath.Join(directory, serverId+".sock"))
				}
				info, err := os.Lstat(directory)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o700 {
					t.Fatalf("directory permissions = %o, want 700", info.Mode().Perm())
				}
				for _, path := range []string{runtime.SocketPath, filepath.Join(directory, "control-"+serverId+".sock")} {
					info, err := os.Lstat(path)
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
						t.Fatalf("socket %s mode = %v, want socket with permissions 600", path, info.Mode())
					}
				}
				entries, err := os.ReadDir(directory)
				if err != nil {
					t.Fatal(err)
				}
				names := make([]string, len(entries))
				generation := regexp.MustCompile(`^server-` + regexp.QuoteMeta(serverId) + `-[0-9a-f]{12}\.sock$`)
				foundGeneration := false
				for i, entry := range entries {
					names[i] = entry.Name()
					if strings.HasPrefix(entry.Name(), ".") {
						t.Errorf("unexpected hidden server entry %q", entry.Name())
					}
					foundGeneration = foundGeneration || generation.MatchString(entry.Name())
				}
				// The three entries are the public, control, and private generation sockets.
				if len(names) != 3 || !slices.Contains(names, serverId+".sock") || !slices.Contains(names, "control-"+serverId+".sock") || !foundGeneration {
					t.Fatalf("server directory entries = %v, want public/control/generation sockets", names)
				}
				result, err := RunClient(t.Context(), ClientCommand{Command: "client"}, RunClientOptions{})
				if err != nil {
					t.Fatal(err)
				}
				listed, ok := result.(ClientListResult)
				if !ok {
					t.Fatalf("client result = %#v, want ClientListResult", result)
				}
				ids := make([]string, len(listed.Sessions))
				for i, session := range listed.Sessions {
					ids[i] = session.SessionId
				}
				if result.Kind() != "list" || !reflect.DeepEqual(ids, []string{"demo-1", "demo-2"}) {
					t.Fatalf("client result = %#v, want list containing demo-1, demo-2", result)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:115.
		{
			name: "rejects a provider without a model",
			run: func(t *testing.T, _ string) {
				runtime, err := StartServer(t.Context(), StartServerOptions{Directory: new(socketDir(t)), Provider: new("anthropic")})
				if runtime != nil {
					trackExperimentalServer(t, runtime)
				}
				if err == nil || !strings.Contains(err.Error(), "provider requires a model") {
					t.Fatalf("start error = %v, want provider requires a model", err)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:121.
		{
			name: "preserves an existing Session model when the server default changes",
			run: func(t *testing.T, agentDir string) {
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"anthropic","defaultModel":"claude-opus-4-6"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				directory := socketDir(t)
				first, err := StartServer(t.Context(), StartServerOptions{Directory: &directory})
				if err != nil {
					t.Fatal(err)
				}
				trackExperimentalServer(t, first)
				firstClient := attachExperimentalClient(t, first, "demo-1")
				if err := firstClient.Dispose(); err != nil {
					t.Fatal(err)
				}
				waitExperimentalWorkerRetired(t, first, "demo-1")
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
				second, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
				if err != nil {
					t.Fatal(err)
				}
				trackExperimentalServer(t, second)
				secondClient := attachExperimentalClient(t, second, "demo-1")
				if err := secondClient.Dispose(); err != nil {
					t.Fatal(err)
				}
				waitExperimentalWorkerRetired(t, second, "demo-1")
				state := readExperimentalSessionState(t, second.SessionDir, "demo-1")
				modelJSON, err := json.Marshal(state.Model)
				if err != nil {
					t.Fatal(err)
				}
				var model map[string]string
				if err := json.Unmarshal(modelJSON, &model); err != nil {
					t.Fatal(err)
				}
				want := map[string]string{"provider": "anthropic", "modelId": "claude-opus-4-6"}
				if !reflect.DeepEqual(model, want) {
					t.Fatalf("persisted model = %#v, want %#v", model, want)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:146.
		{
			name: "rejects model options when discovery selects an existing server",
			run: func(t *testing.T, _ string) {
				directory, _ := makeExperimentalServer(t)
				_, err := RunClient(t.Context(), ClientCommand{Command: "client", Model: new("anthropic/claude-opus-4-6")}, RunClientOptions{Directory: &directory})
				if err == nil || !strings.Contains(err.Error(), "Model selection is only valid when automatically activating a new server") {
					t.Fatalf("existing-server model selection error = %v", err)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:153.
		{
			name: "rechecks an auto-discovered server after a version mismatch",
			run: func(t *testing.T, _ string) {
				directory, runtime := makeExperimentalServer(t)
				rejectNextExperimentalClientConnect(t)
				opened, err := OpenClientRuntime(t.Context(), ClientCommand{Command: "client"}, OpenClientRuntimeOptions{Directory: &directory})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := opened.Dispose(); err != nil {
						t.Error(err)
					}
				})
				ids := make([]string, len(opened.Servers))
				for i, server := range opened.Servers {
					ids[i] = server.Route.ServerId
				}
				if !reflect.DeepEqual(ids, []string{runtime.ServerId}) {
					t.Fatalf("rechecked routes = %v, want [%s]", ids, runtime.ServerId)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:167.
		{
			name: "serializes concurrent cold activation and retires after both clients leave",
			run: func(t *testing.T, _ string) {
				directory := socketDir(t)
				serverId := "00000000-0000-4000-8000-000000000001"
				t.Setenv("PI_SERVER_DIR", directory)
				t.Setenv("PI_SERVER_ID", serverId)
				results := make([]ClientResult, 2)
				failures := make([]error, len(results))
				var clients sync.WaitGroup
				for i := range results {
					clients.Go(func() {
						results[i], failures[i] = RunClient(t.Context(), ClientCommand{Command: "client"}, RunClientOptions{})
					})
				}
				clients.Wait()
				for i, err := range failures {
					if err != nil {
						t.Fatalf("client %d: %v", i, err)
					}
				}
				want := ClientListResult{Sessions: []services.SessionAddress{{ServerId: serverId, SessionId: "demo-1"}, {ServerId: serverId, SessionId: "demo-2"}}}
				if !reflect.DeepEqual(results, []ClientResult{want, want}) {
					t.Fatalf("concurrent client results = %#v, want %#v", results, []ClientResult{want, want})
				}
				for i, result := range results {
					if result.Kind() != "list" {
						t.Fatalf("client %d result kind = %q, want list", i, result.Kind())
					}
				}
				if _, err := os.Lstat(filepath.Join(directory, serverId+".sock")); err != nil {
					t.Fatalf("public socket immediately after both clients leave: %v", err)
				}
				waitExperimentalPathRemoved(t, filepath.Join(directory, serverId+".sock"))
				waitExperimentalPathRemoved(t, filepath.Join(directory, "control-"+serverId+".sock"))
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:196.
		{
			name: "passes client plugin packages to a cold server and restores them for its next generation",
			run: func(t *testing.T, _ string) {
				directory := socketDir(t)
				serverId := "00000000-0000-4000-8000-000000000001"
				packagePath := experimentalExamplePluginPath(t)
				t.Setenv("PI_SERVER_DIR", directory)
				t.Setenv("PI_SERVER_ID", serverId)
				first, err := OpenClientRuntime(t.Context(), ClientCommand{Command: "client", Provider: new("anthropic"), Model: new("claude-sonnet-4-5")}, OpenClientRuntimeOptions{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := first.Dispose(); err != nil {
						t.Error(err)
					}
				})
				activated, err := ActivateBuiltinClientServices(t.Context(), first.Servers[0])
				if err != nil {
					t.Fatal(err)
				}
				presentation, err := activated.Plugins.PrepareSession(t.Context(), services.PrepareSessionPluginsRequest{SessionId: "demo-1", PackagePaths: []string{packagePath}})
				if err != nil {
					t.Fatal(err)
				}
				if err := activated.Management.Attach(t.Context(), "demo-1"); err != nil {
					t.Fatal(err)
				}
				loaders, err := CreatePresentationFacetLoaders(presentation)
				if err != nil {
					t.Fatal(err)
				}
				loaded, err := loaders[0].Load(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				loadedDisposed := false
				t.Cleanup(func() {
					if !loadedDisposed {
						if err := loaded.Dispose(context.Background()); err != nil {
							t.Error(err)
						}
					}
				})
				ids := make([]string, len(loaded.Facets))
				for i, facet := range loaded.Facets {
					ids[i] = facet.Id
				}
				if !reflect.DeepEqual(ids, []string{"@earendil-works/pi-example-plugin/tui"}) {
					t.Fatalf("loaded facet IDs = %v", ids)
				}
				if err := loaded.Dispose(t.Context()); err != nil {
					t.Fatal(err)
				}
				loadedDisposed = true
				if err := first.Dispose(); err != nil {
					t.Fatal(err)
				}
				waitExperimentalPathRemoved(t, filepath.Join(directory, serverId+".sock"))
				second, err := OpenClientRuntime(t.Context(), ClientCommand{Command: "client", Provider: new("anthropic"), Model: new("claude-sonnet-4-5")}, OpenClientRuntimeOptions{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := second.Dispose(); err != nil {
						t.Error(err)
					}
				})
				activated, err = ActivateBuiltinClientServices(t.Context(), second.Servers[0])
				if err != nil {
					t.Fatal(err)
				}
				presentation, err = activated.Plugins.PrepareSession(t.Context(), services.PrepareSessionPluginsRequest{SessionId: "demo-1", PackagePaths: nil})
				if err != nil {
					t.Fatal(err)
				}
				if err := activated.Management.Attach(t.Context(), "demo-1"); err != nil {
					t.Fatal(err)
				}
				loaders, err = CreatePresentationFacetLoaders(presentation)
				if err != nil {
					t.Fatal(err)
				}
				if got := len(loaders); got != 1 {
					t.Fatalf("restored presentation loaders = %d, want 1", got)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:234.
		{
			name: "retires a cold server after its only Session attachment disconnects",
			run: func(t *testing.T, _ string) {
				directory := socketDir(t)
				serverId := "00000000-0000-4000-8000-000000000001"
				t.Setenv("PI_SERVER_DIR", directory)
				t.Setenv("PI_SERVER_ID", serverId)
				result, err := RunClient(t.Context(), ClientCommand{Command: "client", SessionId: new("demo-1"), Provider: new("anthropic"), Model: new("claude-sonnet-4-5")}, RunClientOptions{})
				if err != nil {
					t.Fatal(err)
				}
				want := ClientAttachedResult{ServerId: serverId, SessionId: "demo-1"}
				if !reflect.DeepEqual(result, want) || result.Kind() != "attached" {
					t.Fatalf("client result = %#v, want %#v", result, want)
				}
				waitExperimentalPathRemoved(t, filepath.Join(directory, serverId+".sock"))
				waitExperimentalPathRemoved(t, filepath.Join(directory, "control-"+serverId+".sock"))
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:250.
		{
			name: "runs and discovers multiple logical servers from one directory",
			run: func(t *testing.T, _ string) {
				directory := socketDir(t)
				ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
				runtimes := make([]*RunningServer, len(ids))
				failures := make([]error, len(ids))
				var starts sync.WaitGroup
				for i, id := range ids {
					starts.Go(func() {
						runtimes[i], failures[i] = StartServer(t.Context(), StartServerOptions{Directory: &directory, ServerId: &id})
					})
				}
				starts.Wait()
				for _, runtime := range runtimes {
					if runtime != nil {
						trackExperimentalServer(t, runtime)
					}
				}
				for i, err := range failures {
					if err != nil {
						t.Fatalf("start %s: %v", ids[i], err)
					}
				}
				for _, id := range ids {
					info, err := os.Lstat(filepath.Join(directory, "control-"+id+".sock"))
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode()&os.ModeSocket == 0 {
						t.Fatalf("control path for %s is not a socket", id)
					}
				}
				result, err := RunClient(t.Context(), ClientCommand{Command: "client"}, RunClientOptions{Directory: &directory})
				if err != nil {
					t.Fatal(err)
				}
				want := ClientListResult{Sessions: []services.SessionAddress{
					{ServerId: ids[0], SessionId: "demo-1"}, {ServerId: ids[0], SessionId: "demo-2"},
					{ServerId: ids[1], SessionId: "demo-1"}, {ServerId: ids[1], SessionId: "demo-2"},
				}}
				if !reflect.DeepEqual(result, want) || result.Kind() != "list" {
					t.Fatalf("two-server listing = %#v, want %#v", result, want)
				}
				if err := runtimes[0].Close(); err != nil {
					t.Fatal(err)
				}
				waitExperimentalPathRemoved(t, runtimes[0].SocketPath)
				result, err = RunClient(t.Context(), ClientCommand{Command: "client"}, RunClientOptions{Directory: &directory})
				if err != nil {
					t.Fatal(err)
				}
				want.Sessions = want.Sessions[2:]
				if !reflect.DeepEqual(result, want) || result.Kind() != "list" {
					t.Fatalf("remaining-server listing = %#v, want %#v", result, want)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:285.
		{
			name: "hydrates and mutates server Session services across framed clients",
			run: func(t *testing.T, _ string) {
				_, runtime := makeExperimentalServer(t)
				peers := make([]*client.Client, 2)
				for i := range peers {
					factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: runtime.SocketPath})
					if err != nil {
						t.Fatal(err)
					}
					connected, err := client.Connect(t.Context(), client.ClientOptions{ServerId: runtime.ServerId, TransportFactory: factory})
					if err != nil {
						t.Fatal(err)
					}
					trackExperimentalClient(t, connected)
					peers[i] = connected
				}
				var errorsMu sync.Mutex
				failures := []error{}
				onError := func(err error) {
					errorsMu.Lock()
					defer errorsMu.Unlock()
					failures = append(failures, err)
				}
				definitions := []string{services.SessionDirectoryDefinition.Id(), services.SessionManagementDefinition.Id()}
				first := createServerServiceBinding(t, peers[0], definitions, ClientServiceSourceOptions{OnError: onError})
				second := createServerServiceBinding(t, peers[1], definitions, ClientServiceSourceOptions{OnError: onError})
				firstConnection := first.Connection().Value()
				secondConnection := second.Connection().Value()
				if firstConnection == nil || secondConnection == nil || firstConnection.Status != "connected" || secondConnection.Status != "connected" {
					t.Fatalf("connection states = %#v, %#v", firstConnection, secondConnection)
				}
				catalogue, err := first.Catalogue(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				wantCatalogue := []chord.ServiceCatalogueEntry{
					{ServiceId: services.SessionDirectoryDefinition.Id(), Mode: chord.ServiceSingleton},
					{ServiceId: services.SessionManagementDefinition.Id(), Mode: chord.ServiceSingleton},
					{ServiceId: services.PresentationPluginsDefinition.Id(), Mode: chord.ServiceSingleton},
				}
				if !reflect.DeepEqual(catalogue, wantCatalogue) {
					t.Fatalf("server catalogue = %#v, want %#v", catalogue, wantCatalogue)
				}
				firstDirectory, err := chord.UseRemoteClient(first, services.SessionDirectoryDefinition)
				if err != nil {
					t.Fatal(err)
				}
				secondDirectory, err := chord.UseRemoteClient(second, services.SessionDirectoryDefinition)
				if err != nil {
					t.Fatal(err)
				}
				firstManagement, err := chord.UseRemoteClient(first, services.SessionManagementDefinition)
				if err != nil {
					t.Fatal(err)
				}
				secondManagement, err := chord.UseRemoteClient(second, services.SessionManagementDefinition)
				if err != nil {
					t.Fatal(err)
				}
				ready := make([]error, 2)
				var pending sync.WaitGroup
				pending.Go(func() { ready[0] = first.Ready(t.Context()) })
				pending.Go(func() { ready[1] = second.Ready(t.Context()) })
				pending.Wait()
				for _, err := range ready {
					if err != nil {
						t.Fatal(err)
					}
				}
				state := firstDirectory.State().Value()
				if state == nil {
					t.Fatal("first Session directory did not hydrate")
				}
				ids := make([]string, len(state.Sessions))
				for i, session := range state.Sessions {
					ids[i] = session.SessionId
				}
				if !reflect.DeepEqual(ids, []string{"demo-1", "demo-2"}) || !reflect.DeepEqual(secondDirectory.State().Value(), state) {
					t.Fatalf("initial directories = %#v, %#v", state, secondDirectory.State().Value())
				}
				changes := watchExperimentalChanges(t,
					func(notify func()) (func(), error) {
						return firstDirectory.State().Subscribe(func(*services.SessionDirectoryState, context.Context, pico3.ReplicatedStateDelivery) { notify() })
					},
					func(notify func()) (func(), error) {
						return secondDirectory.State().Subscribe(func(*services.SessionDirectoryState, context.Context, pico3.ReplicatedStateDelivery) { notify() })
					},
					func(notify func()) (func(), error) {
						return peers[0].OnAttachmentChange(client.NewAttachmentChangeListener(func(*protocol.SessionTarget) { notify() }))
					},
					func(notify func()) (func(), error) {
						return peers[1].OnAttachmentChange(client.NewAttachmentChangeListener(func(*protocol.SessionTarget) { notify() }))
					},
				)
				if _, err := firstManagement.Create(t.Context(), services.SessionCreateOptions{Id: new("demo-3")}); err != nil {
					t.Fatal(err)
				}
				changes.Wait(t, func() bool {
					state := firstDirectory.State().Value()
					return state != nil && slices.ContainsFunc(state.Sessions, func(session services.SessionSummary) bool { return session.SessionId == "demo-3" }) && reflect.DeepEqual(secondDirectory.State().Value(), state)
				})
				pending.Go(func() { ready[0] = firstManagement.Attach(t.Context(), "demo-1") })
				pending.Go(func() { ready[1] = secondManagement.Attach(t.Context(), "demo-1") })
				pending.Wait()
				for i, err := range ready {
					if err != nil {
						t.Fatal(err)
					}
					attachment := peers[i].Attachment()
					if attachment == nil || attachment.SessionId != "demo-1" {
						t.Fatalf("peer %d attachment = %#v, want demo-1", i, attachment)
					}
				}
				if err := firstManagement.Remove(t.Context(), "demo-1"); err != nil {
					t.Fatal(err)
				}
				changes.Wait(t, func() bool {
					state := firstDirectory.State().Value()
					return peers[0].Attachment() == nil && peers[1].Attachment() == nil && state != nil && !slices.ContainsFunc(state.Sessions, func(session services.SessionSummary) bool { return session.SessionId == "demo-1" }) && reflect.DeepEqual(secondDirectory.State().Value(), state)
				})
				if err := secondManagement.Detach(t.Context()); err != nil {
					t.Fatal(err)
				}
				errorsMu.Lock()
				observedErrors := slices.Clone(failures)
				errorsMu.Unlock()
				if len(observedErrors) != 0 {
					t.Fatalf("server service errors = %v, want []", observedErrors)
				}
				pending.Go(func() { ready[0] = first.Dispose(t.Context()) })
				pending.Go(func() { ready[1] = second.Dispose(t.Context()) })
				pending.Wait()
				for _, err := range ready {
					if err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:346.
		{
			name: "hydrates and updates the Models service across concurrent framed clients",
			run: func(t *testing.T, _ string) {
				_, runtime := makeExperimentalServer(t)
				firstClient := attachExperimentalClient(t, runtime, "demo-1")
				pid, exists := runtime.WorkerPids()["demo-1"]
				if !exists {
					t.Fatal("first attachment has no worker PID")
				}
				secondClient := attachExperimentalClient(t, runtime, "demo-1")
				if next, exists := runtime.WorkerPids()["demo-1"]; !exists || next != pid {
					t.Fatalf("second attachment worker PID = %d (present %v), want %d", next, exists, pid)
				}
				var errorsMu sync.Mutex
				failures := []error{}
				onError := func(err error) {
					errorsMu.Lock()
					defer errorsMu.Unlock()
					failures = append(failures, err)
				}
				definitions := []string{services.ModelsDefinition.Id()}
				first := createSessionServiceBinding(t, firstClient, definitions, ClientServiceSourceOptions{OnError: onError})
				second := createSessionServiceBinding(t, secondClient, definitions, ClientServiceSourceOptions{OnError: onError})
				firstModels, err := chord.UseRemoteClient(first, services.ModelsDefinition)
				if err != nil {
					t.Fatal(err)
				}
				secondModels, err := chord.UseRemoteClient(second, services.ModelsDefinition)
				if err != nil {
					t.Fatal(err)
				}
				ready := make([]error, 2)
				var pending sync.WaitGroup
				pending.Go(func() { ready[0] = first.Ready(t.Context()) })
				pending.Go(func() { ready[1] = second.Ready(t.Context()) })
				pending.Wait()
				for _, err := range ready {
					if err != nil {
						t.Fatal(err)
					}
				}
				wantAttachment := &services.SessionAttachmentState{Status: "attached", SessionID: "demo-1"}
				if !reflect.DeepEqual(first.Attachment().Value(), wantAttachment) || !reflect.DeepEqual(second.Attachment().Value(), wantAttachment) {
					t.Fatalf("binding attachments = %#v, %#v, want %#v", first.Attachment().Value(), second.Attachment().Value(), wantAttachment)
				}
				state := firstModels.State().Value()
				wantModel := &services.ModelRef{Provider: "anthropic", ModelId: "claude-sonnet-4-5"}
				if state == nil || !reflect.DeepEqual(state.Configuration.Model, wantModel) || !reflect.DeepEqual(secondModels.State().Value(), state) {
					t.Fatalf("hydrated Models states = %#v, %#v", state, secondModels.State().Value())
				}
				previousThinking := state.Configuration.ThinkingLevel
				changes := watchExperimentalChanges(t,
					func(notify func()) (func(), error) {
						return firstModels.State().Subscribe(func(*services.ModelsState, context.Context, pico3.ReplicatedStateDelivery) { notify() })
					},
					func(notify func()) (func(), error) {
						return secondModels.State().Subscribe(func(*services.ModelsState, context.Context, pico3.ReplicatedStateDelivery) { notify() })
					},
				)
				if err := firstModels.CycleThinking(t.Context()); err != nil {
					t.Fatal(err)
				}
				changes.Wait(t, func() bool {
					state := firstModels.State().Value()
					return state != nil && state.Configuration.ThinkingLevel != previousThinking && reflect.DeepEqual(secondModels.State().Value(), state)
				})
				errorsMu.Lock()
				observedErrors := slices.Clone(failures)
				errorsMu.Unlock()
				if len(observedErrors) != 0 {
					t.Fatalf("Models errors = %v, want []", observedErrors)
				}
				pending.Go(func() { ready[0] = first.Dispose(t.Context()) })
				pending.Go(func() { ready[1] = second.Dispose(t.Context()) })
				pending.Wait()
				for _, err := range ready {
					if err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:384.
		{
			name: "loads conventional Session facets from multiple configured plugin packages",
			run: func(t *testing.T, _ string) {
				directory := socketDir(t)
				secondPackagePath := filepath.Join(directory, "second-plugin")
				if err := os.MkdirAll(filepath.Join(secondPackagePath, "src"), 0o700); err != nil {
					t.Fatal(err)
				}
				files := []struct{ path, text string }{
					{filepath.Join(secondPackagePath, "package.json"), "{\"name\":\"@earendil-works/second-session-plugin\",\"version\":\"1.0.0\",\"peerDependencies\":{\"@earendil-works/chord\":\"^0.84.4\"}}\n"},
					{filepath.Join(secondPackagePath, "src", "session.ts"), "import { defineFacet, defineService } from \"@earendil-works/chord\"; const Service = defineService(\"test.second-plugin\"); export default defineFacet({ id: \"second-session-plugin\", setup(env) { env.provide(Service, { async read() { return \"second\"; } }); } });\n"},
				}
				writeErrors := make([]error, len(files))
				var writes sync.WaitGroup
				for i, file := range files {
					writes.Go(func() { writeErrors[i] = os.WriteFile(file.path, []byte(file.text), 0o600) })
				}
				writes.Wait()
				for i, err := range writeErrors {
					if err != nil {
						t.Fatalf("write %s: %v", files[i].path, err)
					}
				}
				runtime, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
				if err != nil {
					t.Fatal(err)
				}
				trackExperimentalServer(t, runtime)
				opened, err := OpenClientRuntime(t.Context(), ClientCommand{Command: "client"}, OpenClientRuntimeOptions{Directory: &directory})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := opened.Dispose(); err != nil {
						t.Error(err)
					}
				})
				activated, err := ActivateBuiltinClientServices(t.Context(), opened.Servers[0])
				if err != nil {
					t.Fatal(err)
				}
				if _, err := activated.Plugins.PrepareSession(t.Context(), services.PrepareSessionPluginsRequest{SessionId: "demo-1", PackagePaths: []string{experimentalExamplePluginPath(t), secondPackagePath}}); err != nil {
					t.Fatal(err)
				}
				if err := activated.Management.Attach(t.Context(), "demo-1"); err != nil {
					t.Fatal(err)
				}
				// upstream: packages/coding-agent/examples/plugins/pi-example-plugin/src/contract.ts:13.
				binding := createSessionServiceBinding(t, opened.Servers[0].Client, []string{"pi.example-plugin.greeting", "test.second-plugin"}, ClientServiceSourceOptions{})
				if err := binding.Ready(t.Context()); err != nil {
					t.Fatal(err)
				}
				example, err := binding.Use("pi.example-plugin.greeting")
				if err != nil {
					t.Fatal(err)
				}
				greeting, err := chord.CallResult[struct {
					WorkerActivations float64 `json:"workerActivations"`
				}](t.Context(), example, "greet", map[string]string{"name": "Armin"})
				if err != nil {
					t.Fatal(err)
				}
				if greeting.WorkerActivations != 1 {
					t.Fatalf("example plugin worker activations = %v, want 1", greeting.WorkerActivations)
				}
				secondPlugin, err := binding.Use("test.second-plugin")
				if err != nil {
					t.Fatal(err)
				}
				value, err := chord.CallResult[string](t.Context(), secondPlugin, "read")
				if err != nil || value != "second" {
					t.Fatalf("second plugin read = %q, %v; want second", value, err)
				}
				if err := binding.Dispose(t.Context()); err != nil {
					t.Fatal(err)
				}
			},
		},
		// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:433.
		{
			name: "uses the most recently selected model for a new Session",
			run: func(t *testing.T, _ string) {
				directory := socketDir(t)
				runtime, err := StartServer(t.Context(), StartServerOptions{Directory: &directory})
				if err != nil {
					t.Fatal(err)
				}
				trackExperimentalServer(t, runtime)
				firstClient := attachExperimentalClient(t, runtime, "demo-1")
				first := createSessionServiceBinding(t, firstClient, []string{services.ModelsDefinition.Id()}, ClientServiceSourceOptions{})
				firstModels, err := chord.UseRemoteClient(first, services.ModelsDefinition)
				if err != nil {
					t.Fatal(err)
				}
				if err := first.Ready(t.Context()); err != nil {
					t.Fatal(err)
				}
				wantModel := services.ModelRef{Provider: "anthropic", ModelId: "claude-opus-4-6"}
				if err := firstModels.Select(t.Context(), wantModel); err != nil {
					t.Fatal(err)
				}
				if err := first.Dispose(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := firstClient.Dispose(); err != nil {
					t.Fatal(err)
				}
				waitExperimentalWorkerRetired(t, runtime, "demo-1")
				secondClient := attachExperimentalClient(t, runtime, "demo-2")
				second := createSessionServiceBinding(t, secondClient, []string{services.ModelsDefinition.Id()}, ClientServiceSourceOptions{})
				secondModels, err := chord.UseRemoteClient(second, services.ModelsDefinition)
				if err != nil {
					t.Fatal(err)
				}
				if err := second.Ready(t.Context()); err != nil {
					t.Fatal(err)
				}
				state := secondModels.State().Value()
				if state == nil || !reflect.DeepEqual(state.Configuration.Model, &wantModel) {
					t.Fatalf("second Session Models state = %#v, want selected model %#v", state, wantModel)
				}
				if err := second.Dispose(t.Context()); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:38.
			agentDir := setupExperimentalRemoteTest(t)
			test.run(t, agentDir)
		})
	}
}
