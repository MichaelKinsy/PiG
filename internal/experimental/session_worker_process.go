package experimental

// Ports packages/coding-agent/src/experimental/session-worker.ts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

const (
	SessionWorkerControlAddressEnv     = "PI_SESSION_WORKER_CONTROL_ADDRESS"
	SessionWorkerControlTokenEnv       = "PI_SESSION_WORKER_CONTROL_TOKEN"
	SessionWorkerSessionKeyEnv         = "PI_SESSION_WORKER_SESSION_KEY_BASE64"
	SessionWorkerPeerIDEnv             = "PI_SESSION_WORKER_PEER_ID"
	SessionWorkerInitialDemandGraceEnv = "__PI_SESSION_WORKER_INITIAL_DEMAND_GRACE_MS"
	SessionWorkerOrphanDemandGraceEnv  = "__PI_SESSION_WORKER_ORPHAN_DEMAND_GRACE_MS"
)

// SessionWorkerOptions describes the durable Session and selected plugins of one worker process.
type SessionWorkerOptions struct {
	SessionDir          string                  `json:"sessionDir"`
	Metadata            session.SessionMetadata `json:"metadata"`
	Provider            string                  `json:"provider,omitempty"`
	Model               string                  `json:"model,omitempty"`
	PluginManifestPaths []string                `json:"pluginManifestPaths"`
}

// SessionWorkerHarness is the durable Harness boundary owned by a worker process. Lane returns the presentation adapter for the acquired durable lane.
type SessionWorkerHarness interface {
	Events() *agentharness.HarnessEventBus
	Lane(context.Context, string) (services.SessionWorkerServiceLane, error)
	Close(context.Context) error
}

// SessionWorkerRuntime selects a Harness and its optional already-acquired lane and facet collaborators.
type SessionWorkerRuntime struct {
	Harness         SessionWorkerHarness
	Lane            services.SessionWorkerServiceLane
	ModelRuntime    services.ModelsServiceModelRuntime
	SettingsManager services.ModelsServiceSettingsManager
	FacetLoader     chord.FacetLoader
}
type CreateSessionWorkerHarness func(context.Context, session.Session, SessionWorkerOptions, *envpkg.NodeExecutionEnv) (SessionWorkerRuntime, error)

type workerControlConnection struct {
	socket                    net.Conn
	mu                        sync.Mutex
	messages                  chan json.RawMessage
	done                      chan struct{}
	ctx                       context.Context
	cancel                    context.CancelFunc
	initialServerConnectionID *string
}

func (control *workerControlConnection) send(payload any) error {
	control.mu.Lock()
	defer control.mu.Unlock()
	return writeControlLine(control.socket, map[string]any{"type": "send", "to": "server", "payload": payload})
}
func (control *workerControlConnection) close() {
	control.cancel()
	_ = control.socket.Close()
	<-control.done
}
func connectWorkerControl(ctx context.Context) (*workerControlConnection, error) {
	address, token, key := os.Getenv(SessionWorkerControlAddressEnv), os.Getenv(SessionWorkerControlTokenEnv), os.Getenv(SessionWorkerSessionKeyEnv)
	if address == "" || token == "" || key == "" {
		return nil, errors.New("Session worker requires a control address")
	}
	peerID := os.Getenv(SessionWorkerPeerIDEnv)
	if peerID == "" {
		return nil, errors.New("Session worker requires a peer ID")
	}
	socket, err := (&net.Dialer{}).DialContext(ctx, "unix", address)
	if err != nil {
		return nil, err
	}
	controlCtx, cancel := context.WithCancel(ctx)
	stopClosing := context.AfterFunc(controlCtx, func() { _ = socket.Close() })
	control := &workerControlConnection{socket: socket, messages: make(chan json.RawMessage), done: make(chan struct{}), ctx: controlCtx, cancel: cancel}
	go func() {
		defer stopClosing()
		defer close(control.done)
		defer close(control.messages)
		defer cancel()
		_ = readControlLines(socket, func(line json.RawMessage) error {
			copied := append(json.RawMessage(nil), line...)
			select {
			case control.messages <- copied:
				return nil
			case <-controlCtx.Done():
				return context.Cause(controlCtx)
			}
		})
	}()
	if err := writeControlLine(socket, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": peerID}); err != nil {
		control.close()
		return nil, err
	}
	var first json.RawMessage
	select {
	case first = <-control.messages:
	case <-controlCtx.Done():
		control.close()
		return nil, context.Cause(controlCtx)
	}
	var fields map[string]json.RawMessage
	var message controlMessage
	if json.Unmarshal(first, &fields) != nil || json.Unmarshal(first, &message) != nil || message.Type != "peer_registered" || !stringMember(fields, "peerId") {
		control.close()
		return nil, errors.New("Coordinator rejected the session worker registration")
	}
	if raw, present := fields["serverConnectionId"]; present {
		if !stringMember(fields, "serverConnectionId") {
			control.close()
			return nil, errors.New("Coordinator rejected the session worker registration")
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			control.close()
			return nil, err
		}
		control.initialServerConnectionID = &id
	}
	return control, nil
}

func parseSessionWorkerOptions(args []string) (SessionWorkerOptions, error) {
	var options SessionWorkerOptions
	if len(args) != 1 {
		return options, errors.New("Session worker requires one options argument")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args[0]), &fields); err != nil {
		// session-worker.ts:781 throws Error("Session worker received invalid options", { cause }): the message excludes the parse error.
		return options, facetError("Session worker received invalid options", err)
	}
	invalid := errors.New("Session worker received invalid options")
	var wire struct {
		SessionDir          string          `json:"sessionDir"`
		Metadata            json.RawMessage `json:"metadata"`
		Provider            string          `json:"provider"`
		Model               string          `json:"model"`
		PluginManifestPaths []string        `json:"pluginManifestPaths"`
	}
	metadata, metadataValid := decodeSessionWorkerMetadata(fields["metadata"])
	if json.Unmarshal([]byte(args[0]), &wire) != nil || !metadataValid {
		return options, invalid
	}
	options = SessionWorkerOptions{SessionDir: wire.SessionDir, Metadata: metadata, Provider: wire.Provider, Model: wire.Model, PluginManifestPaths: wire.PluginManifestPaths}
	if options.SessionDir == "" || options.PluginManifestPaths == nil || !filepath.IsAbs(options.SessionDir) || !filepath.IsAbs(options.Metadata.Cwd) || !filepath.IsAbs(options.Metadata.Path) {
		return options, invalid
	}
	for key := range fields {
		switch key {
		case "sessionDir", "metadata", "provider", "model", "pluginManifestPaths":
		default:
			return options, invalid
		}
	}
	for _, key := range []string{"provider", "model"} {
		if raw, ok := fields[key]; ok && (!stringMember(fields, key) || string(raw) == `""`) {
			return options, invalid
		}
	}
	if _, providerPresent := fields["provider"]; providerPresent {
		if _, modelPresent := fields["model"]; !modelPresent {
			return options, invalid
		}
	}
	if slices.Contains(options.PluginManifestPaths, "") {
		return options, invalid
	}
	return options, nil
}

// decodeWorkerSessionKey mirrors Buffer.from(value, "base64url"): both alphabets and omitted padding are accepted, and non-alphabet characters are ignored.
func decodeWorkerSessionKey(value string) string {
	value, _, _ = strings.Cut(value, "=")
	alphabet := strings.Map(func(r rune) rune {
		switch {
		case r == '-':
			return '+'
		case r == '_':
			return '/'
		case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/':
			return r
		default:
			return -1
		}
	}, value)
	if len(alphabet)%4 == 1 {
		alphabet = alphabet[:len(alphabet)-1]
	}
	decoded, _ := base64.RawStdEncoding.DecodeString(alphabet)
	return string(decoded)
}

// maxSafeInteger is Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 9_007_199_254_740_991

func workerLifecycleDelay(name string, fallback int) (int, error) {
	raw, present := os.LookupEnv(name)
	if !present {
		return fallback, nil
	}
	value := trimECMAScript(raw)
	number := float64(0)
	var err error
	// Number() has no numeric separators; strconv accepts underscores after a base prefix and in ParseFloat.
	if strings.Contains(value, "_") {
		err = strconv.ErrSyntax
	} else if value != "" {
		if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") || strings.HasPrefix(value, "0o") || strings.HasPrefix(value, "0O") || strings.HasPrefix(value, "0b") || strings.HasPrefix(value, "0B") {
			n, parseErr := strconv.ParseUint(value, 0, 64)
			if parseErr != nil || n > maxSafeInteger || n > math.MaxInt {
				return 0, fmt.Errorf("%s must be a non-negative safe integer", name)
			}
			return int(n), nil
		} else {
			number, err = strconv.ParseFloat(value, 64)
		}
	}
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > maxSafeInteger || number > math.MaxInt || math.Trunc(number) != number {
		return 0, fmt.Errorf("%s must be a non-negative safe integer", name)
	}
	return int(number), nil
}

// reportWorkerOutcome prints a post-startup cleanup failure, or sends worker_failed for a rejected run, and only then releases ownership that Node would keep until process exit.
func reportWorkerOutcome(send func(any) error, token, sessionKey string, result error, cleanupOnly bool, releaseAfterReport func() error) {
	if cleanupOnly {
		fmt.Fprintln(os.Stderr, result)
	} else if result != nil {
		_ = send(map[string]any{"type": "worker_failed", "token": token, "sessionKey": sessionKey, "message": result.Error()})
	}
	if releaseAfterReport != nil {
		_ = releaseAfterReport()
	}
}

// acquireSessionOwnership takes the Session file's ownership lock; a compromised lock terminates the process.
// upstream: packages/coding-agent/src/experimental/session-worker.ts:533-538
func acquireSessionOwnership(path string) (*pilock.Lock, error) {
	return pilock.AcquireWithOptions(context.Background(), path, pilock.AcquireOptions{Stale: 2_000 * time.Millisecond, Update: 1_000 * time.Millisecond, Retry: 25 * time.Millisecond, Wait: 8_000 * time.Millisecond, OnCompromised: lockCompromised})
}

// RunSessionWorkerWithHarness owns the child-process event loop until retirement, shutdown, disconnection, or cancellation. It joins the worker's resources before returning and leaves the control socket to process exit; call it only from a process entry that exits after it returns.
func RunSessionWorkerWithHarness(ctx context.Context, args []string, createHarness CreateSessionWorkerHarness) (result error) {
	options, err := parseSessionWorkerOptions(args)
	if err != nil {
		return err
	}
	control, err := connectWorkerControl(ctx)
	if err != nil {
		return err
	}
	// session-worker.ts never closes its control socket; process.exit closes it after exit teardown, so the coordinator reports disconnection only when the worker is exiting. The caller exits the process after return.
	token := os.Getenv(SessionWorkerControlTokenEnv)
	sessionKey := decodeWorkerSessionKey(os.Getenv(SessionWorkerSessionKeyEnv))
	// A cleanup failure after startup is closeAndExit's rejection branch (session-worker.ts:591-599): the error is printed to stderr and the exit code is 1, with no worker_failed message. The process entry prints no other worker failure (session-worker.ts:883).
	started, cleanupOnly, unreportedCleanup := false, false, false
	// releaseAfterReport holds the ownership lock through the worker_failed send when run() rejected before closeResources: Node keeps proper-lockfile ownership until process exit (session-worker.ts:604-605,790-800).
	var releaseAfterReport func() error
	defer func() {
		reportWorkerOutcome(control.send, token, sessionKey, result, cleanupOnly, releaseAfterReport)
	}()
	path, err := filepath.EvalSymlinks(options.Metadata.Path)
	if err != nil {
		return err
	}
	ownership, err := acquireSessionOwnership(path)
	if err != nil {
		return err
	}
	executionEnv := envpkg.NewNodeExecutionEnv(envpkg.NodeExecutionEnvOptions{Cwd: options.Metadata.Cwd})
	repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: executionEnv, SessionsRoot: options.SessionDir})
	var stored session.Session
	var runtime SessionWorkerRuntime
	var workerServices *services.SessionWorkerServices
	defer func() {
		var failures []any
		record := func(err error) {
			if err != nil {
				failures = append(failures, err)
			}
		}
		if workerServices != nil {
			record(workerServices.Dispose())
		}
		if runtime.Harness != nil {
			record(runtime.Harness.Close(context.Background()))
		} else if stored != nil {
			record(stored.Close(context.Background()))
		}
		record(repo.Close(context.Background()))
		executionEnv.Cleanup(context.Background())
		if unreportedCleanup {
			releaseAfterReport = ownership.Release
		} else {
			record(ownership.Release())
		}
		var cleanupError error
		switch len(failures) {
		case 0:
		case 1:
			cleanupError = failures[0].(error)
		default:
			cleanupError = &services.AggregateError{Message: "Session worker cleanup failed", Errors: failures}
		}
		switch {
		case cleanupError == nil:
		case unreportedCleanup:
		case result == nil:
			result, cleanupOnly = cleanupError, started
		case started:
			result = &services.AggregateError{Message: "Session worker readiness and cleanup failed", Errors: []any{result, cleanupError}}
		default:
			result = &services.AggregateError{Message: "Session worker startup and cleanup failed", Errors: []any{result, cleanupError}}
		}
	}()
	stored, err = repo.Open(context.Background(), options.Metadata)
	if err != nil {
		return err
	}
	runtime, err = createHarness(context.Background(), stored, options, executionEnv)
	if err != nil {
		return err
	}
	lane := runtime.Lane
	if lane == nil {
		lane, err = runtime.Harness.Lane(context.Background(), "main")
		if err != nil {
			return err
		}
	}
	workerServices, err = services.CreateSessionWorkerServices(services.SessionWorkerServicesOptions{Lane: lane, ModelRuntime: runtime.ModelRuntime, SettingsManager: runtime.SettingsManager, FacetLoader: runtime.FacetLoader, Publish: func(_ context.Context, scope services.WorkerServiceScope, subscriptionID string, update chord.ServiceProviderUpdate) error {
		return control.send(map[string]any{"type": "service_update", "token": token, "sessionKey": sessionKey, "scope": WorkerOperationScope{ServerConnectionID: scope.ServerConnectionId, AttachmentID: scope.AttachmentId}, "subscriptionId": subscriptionID, "update": update})
	}})
	if err != nil {
		return err
	}
	// session-worker.ts:604-605 parses the grace delays outside startup's try/catch, so an invalid value rejects run() with its own message and never reaches closeResources; process exit releases what Node leaves open. Go still releases the resources but reports only the delay error.
	unreportedCleanup = true
	initialGrace, err := workerLifecycleDelay(SessionWorkerInitialDemandGraceEnv, 10_000)
	if err != nil {
		return err
	}
	orphanGrace, err := workerLifecycleDelay(SessionWorkerOrphanDemandGraceEnv, 30_000)
	if err != nil {
		return err
	}
	unreportedCleanup, started = false, true
	retired := make(chan struct{})
	var retireOnce sync.Once
	retire := func() { retireOnce.Do(func() { close(retired) }) }
	lifecycle := NewWorkerLifecycle(WorkerLifecycleOptions{InitialServerConnectionID: control.initialServerConnectionID, InitialDemandGraceMs: initialGrace, OrphanDemandGraceMs: orphanGrace, OnRetire: retire})
	defer lifecycle.Close()
	remove, err := installWorkerLifecycle(runtime.Harness.Events(), lifecycle, retire)
	if err != nil {
		return err
	}
	defer remove()
	requests := &workerActiveRequests{values: make(map[string]*workerActiveRequest)}
	// session-worker.ts:579-599 cancels requests and closes resources before process.exit closes the control socket; the coordinator observes disconnection only after cleanup.
	defer requests.close()
	// session-worker.ts:747-748 closes resources and exits on the first SIGTERM or SIGINT once startup completes.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	announce := func() error {
		return control.send(map[string]any{"type": "worker_ready", "token": token, "sessionKey": sessionKey, "sessionId": options.Metadata.ID, "pid": os.Getpid(), "metadata": newSessionWorkerMetadata(options.Metadata), "pluginManifestPaths": options.PluginManifestPaths})
	}
	if err := announce(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-retired:
			return nil
		case <-signals:
			return nil
		case raw, open := <-control.messages:
			if !open {
				return nil
			}
			if err := handleWorkerCommand(raw, control, lifecycle, requests, workerServices, token, sessionKey, announce, retire); err != nil {
				return nil
			}
		}
	}
}

func installWorkerLifecycle(events *agentharness.HarnessEventBus, lifecycle *WorkerLifecycle, retire func()) (func(), error) {
	var removers []func()
	remove := func() {
		for _, fn := range removers {
			fn()
		}
	}
	for _, eventType := range []agentharness.HarnessEventType{agentharness.EventRunStart, agentharness.EventRunResume, agentharness.EventRunSuspend, agentharness.EventRunEnd, agentharness.EventCompactionStart, agentharness.EventCompactionEnd, agentharness.EventNavigationStart, agentharness.EventNavigationEnd, agentharness.EventFault} {
		unsubscribe, err := events.On(eventType, func(_ context.Context, event agentharness.HarnessEvent) error {
			switch payload := event.Payload.(type) {
			case agentharness.RunStartPayload:
				lifecycle.OperationStarted("run", event.Lane, payload.RunID)
			case agentharness.RunResumePayload:
				lifecycle.OperationStarted("run", event.Lane, payload.RunID)
			case agentharness.RunSuspendPayload:
				lifecycle.OperationStopped("run", event.Lane, payload.RunID)
			case agentharness.RunEndPayload:
				lifecycle.OperationStopped("run", event.Lane, payload.RunID)
			case agentharness.CompactionStartPayload:
				lifecycle.OperationStarted("compaction", event.Lane, payload.RunID)
			case agentharness.CompactionEndPayload:
				lifecycle.OperationStopped("compaction", event.Lane, payload.RunID)
			case agentharness.NavigationStartPayload:
				lifecycle.OperationStarted("navigation", event.Lane, payload.RunID)
			case agentharness.NavigationEndPayload:
				lifecycle.OperationStopped("navigation", event.Lane, payload.RunID)
			case agentharness.FaultPayload:
				retire()
			}
			return nil
		})
		if err != nil {
			remove()
			return nil, err
		}
		removers = append(removers, unsubscribe)
	}
	return remove, nil
}
