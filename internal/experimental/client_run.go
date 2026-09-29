package experimental

// Ports packages/coding-agent/src/experimental/client.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// ClientResult is the closed list/attached/prompted result union. Go names the upstream anonymous variants so callers cannot confuse absent fields with zero values.
type ClientResult interface {
	Kind() string
	clientResult()
}

type ClientListResult struct {
	Sessions []services.SessionAddress `json:"sessions"`
}

func (ClientListResult) Kind() string  { return "list" }
func (ClientListResult) clientResult() {}
func (result ClientListResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind     string                    `json:"kind"`
		Sessions []services.SessionAddress `json:"sessions"`
	}{result.Kind(), result.Sessions})
}

type ClientAttachedResult struct {
	ServerId  string `json:"serverId"`
	SessionId string `json:"sessionId"`
}

func (ClientAttachedResult) Kind() string  { return "attached" }
func (ClientAttachedResult) clientResult() {}
func (result ClientAttachedResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind      string `json:"kind"`
		ServerId  string `json:"serverId"`
		SessionId string `json:"sessionId"`
	}{result.Kind(), result.ServerId, result.SessionId})
}

type ClientPromptedResult struct {
	ServerId  string `json:"serverId"`
	SessionId string `json:"sessionId"`
	Text      string `json:"text"`
}

func (ClientPromptedResult) Kind() string  { return "prompted" }
func (ClientPromptedResult) clientResult() {}
func (result ClientPromptedResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind      string `json:"kind"`
		ServerId  string `json:"serverId"`
		SessionId string `json:"sessionId"`
		Text      string `json:"text"`
	}{result.Kind(), result.ServerId, result.SessionId, result.Text})
}

// RunClientOptions selects discovery and snapshot-ordered event delivery. OnEvent receives the exact LaneWatchEvent JSON already carried by TranscriptState, without reconstructing a separate event union. Each callback is awaited before the next; its error propagates from RunClient.
type RunClientOptions struct {
	Directory *string
	OnEvent   func(context.Context, json.RawMessage) error
}

// RunClient discovers servers, then lists, attaches or creates a Session and runs a one-shot prompt. ctx owns initial opening; service operations retain upstream's Background Context. Prompt completion waits independently for the terminal transcript event and drains ordered callback delivery before disposing the runtime.
func RunClient(ctx context.Context, command ClientCommand, options RunClientOptions) (result ClientResult, err error) {
	runtime, err := OpenClientRuntime(ctx, command, OpenClientRuntimeOptions{Directory: options.Directory})
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanup := runtime.Dispose(); cleanup != nil {
			result, err = nil, cleanup
		}
	}()
	discovered := make([]*ActivatedClientRuntimeServer, len(runtime.Servers))
	var first sync.Once
	var failure error
	var tasks sync.WaitGroup
	for index, server := range runtime.Servers {
		tasks.Go(func() {
			activated, err := ActivateBuiltinClientServices(ctx, server)
			if err != nil {
				first.Do(func() { failure = err })
				return
			}
			discovered[index] = activated
		})
	}
	tasks.Wait()
	if failure != nil {
		return nil, failure
	}
	if command.SessionId == nil && command.Prompt == nil {
		sessions := []services.SessionAddress{}
		for _, server := range discovered {
			for _, session := range server.Directory.State().Value().Sessions {
				sessions = append(sessions, services.SessionAddress{ServerId: server.Route.ServerId, SessionId: session.SessionId})
			}
		}
		order := collate.New(language.Und)
		slices.SortStableFunc(sessions, func(left, right services.SessionAddress) int {
			if comparison := order.CompareString(left.ServerId, right.ServerId); comparison != 0 {
				return comparison
			}
			return order.CompareString(left.SessionId, right.SessionId)
		})
		return ClientListResult{Sessions: sessions}, nil
	}
	match, sessionId, err := selectClientSession(command, discovered)
	if err != nil {
		return nil, err
	}
	var packagePaths []string
	if command.PluginPackages != nil {
		packagePaths = make([]string, len(command.PluginPackages))
		for index, path := range command.PluginPackages {
			packagePaths[index], err = filepath.Abs(path)
			if err != nil {
				return nil, err
			}
		}
	}
	if _, err := match.Plugins.PrepareSession(context.Background(), services.PrepareSessionPluginsRequest{SessionId: sessionId, PackagePaths: packagePaths}); err != nil {
		return nil, err
	}
	if err := match.Management.Attach(context.Background(), sessionId); err != nil {
		return nil, err
	}
	if command.Prompt == nil {
		return ClientAttachedResult{ServerId: match.Route.ServerId, SessionId: sessionId}, nil
	}
	text, err := promptClientSession(match, *command.Prompt, options.OnEvent)
	if err != nil {
		return nil, err
	}
	return ClientPromptedResult{ServerId: match.Route.ServerId, SessionId: sessionId, Text: text}, nil
}

func selectClientSession(command ClientCommand, discovered []*ActivatedClientRuntimeServer) (*ActivatedClientRuntimeServer, string, error) {
	if command.SessionId == nil {
		if len(discovered) != 1 {
			return nil, "", errors.New("Client prompt requires exactly one discovered server to create a Session")
		}
		created, err := discovered[0].Management.Create(context.Background(), services.SessionCreateOptions{})
		return discovered[0], created.SessionId, err
	}
	id := *command.SessionId
	var matches []*ActivatedClientRuntimeServer
	for _, server := range discovered {
		if slices.ContainsFunc(server.Directory.State().Value().Sessions, func(session services.SessionSummary) bool { return session.SessionId == id }) {
			matches = append(matches, server)
		}
	}
	if len(matches) > 1 {
		return nil, "", fmt.Errorf("Session %s is available from more than one server", id)
	}
	if len(matches) == 1 {
		return matches[0], id, nil
	}
	if command.Prompt == nil || len(discovered) != 1 {
		return nil, "", fmt.Errorf("No discovered server contains session %s", id)
	}
	_, err := discovered[0].Management.Create(context.Background(), services.SessionCreateOptions{Id: &id})
	return discovered[0], id, err
}

func promptClientSession(server *ActivatedClientRuntimeServer, prompt string, onEvent func(context.Context, json.RawMessage) error) (text string, err error) {
	delivery := newClientEventDelivery(onEvent)
	remove, err := server.Transcript.State().Subscribe(func(value *services.TranscriptState, _ context.Context, update pico3.ReplicatedStateDelivery) {
		if update.Kind == "update" && value != nil && len(value.Event) != 0 && string(value.Event) != "null" {
			delivery.enqueue(value.Event)
		}
	})
	if err != nil {
		_ = delivery.close()
		return "", err
	}
	defer func() {
		remove()
		if failure := delivery.close(); failure != nil {
			text, err = "", failure
		}
	}()
	if state := server.Transcript.State().Value(); state == nil || state.Snapshot == nil {
		return "", errors.New("Transcript has no initialized snapshot")
	}
	response, err := server.Agent.Prompt(context.Background(), services.AgentPromptRequest{Message: prompt, Images: nil})
	if err != nil {
		return "", err
	}
	if response.Accepted {
		if response.OperationID == nil {
			return "", errors.New("Accepted AgentController operation has no operation ID")
		}
		if err := delivery.waitBoundary(*response.OperationID); err != nil {
			return "", err
		}
	}
	remove()
	if err := delivery.close(); err != nil {
		return "", err
	}
	if !response.Accepted || response.Error != nil {
		if response.Error == nil {
			return "", errors.New("Rejected AgentController operation has no error")
		}
		return "", errors.New(response.Error.Message)
	}
	return delivery.completedText[*response.OperationID], nil
}

// clientEventDelivery mirrors the upstream deliveryTail Promise chain: protocol events may arrive ahead of an awaited callback. One owned worker drains those events without blocking the protocol reader, and close joins its completion.
type clientEventDelivery struct {
	mu              sync.Mutex
	ready           *sync.Cond
	queue           []json.RawMessage
	closed          bool
	done            chan struct{}
	failure         error
	boundaryError   error
	completedText   map[string]string
	boundaries      map[string]bool
	boundaryChanged chan struct{}
	onEvent         func(context.Context, json.RawMessage) error
}

func newClientEventDelivery(onEvent func(context.Context, json.RawMessage) error) *clientEventDelivery {
	delivery := &clientEventDelivery{done: make(chan struct{}), completedText: map[string]string{}, boundaries: map[string]bool{}, boundaryChanged: make(chan struct{}), onEvent: onEvent}
	delivery.ready = sync.NewCond(&delivery.mu)
	go delivery.run()
	return delivery
}

func (delivery *clientEventDelivery) enqueue(raw json.RawMessage) {
	var envelope struct {
		Type  string `json:"type"`
		RunId string `json:"runId"`
	}
	err := json.Unmarshal(raw, &envelope)
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if delivery.closed {
		return
	}
	if err != nil {
		delivery.boundaryError = err
		if delivery.failure == nil {
			delivery.failure = err
		}
	} else {
		delivery.queue = append(delivery.queue, slices.Clone(raw))
		if envelope.Type == "run_end" || envelope.Type == "run_suspend" {
			delivery.boundaries[envelope.RunId] = true
		}
	}
	close(delivery.boundaryChanged)
	delivery.boundaryChanged = make(chan struct{})
	delivery.ready.Signal()
}

func (delivery *clientEventDelivery) waitBoundary(id string) error {
	for {
		delivery.mu.Lock()
		ended, failure, changed := delivery.boundaries[id], delivery.boundaryError, delivery.boundaryChanged
		delivery.mu.Unlock()
		if failure != nil {
			return failure
		}
		if ended {
			return nil
		}
		<-changed
	}
}

func (delivery *clientEventDelivery) close() error {
	delivery.mu.Lock()
	delivery.closed = true
	delivery.ready.Broadcast()
	delivery.mu.Unlock()
	<-delivery.done
	return delivery.failure
}

func (delivery *clientEventDelivery) run() {
	defer close(delivery.done)
	for {
		delivery.mu.Lock()
		for len(delivery.queue) == 0 && !delivery.closed {
			delivery.ready.Wait()
		}
		if len(delivery.queue) == 0 {
			delivery.mu.Unlock()
			return
		}
		raw := delivery.queue[0]
		delivery.queue[0] = nil
		delivery.queue = delivery.queue[1:]
		failed := delivery.failure != nil
		delivery.mu.Unlock()
		if failed {
			continue
		}
		if err := delivery.deliver(raw); err != nil {
			delivery.mu.Lock()
			if delivery.failure == nil {
				delivery.failure = err
			}
			close(delivery.boundaryChanged)
			delivery.boundaryChanged = make(chan struct{})
			delivery.mu.Unlock()
		}
	}
}

func (delivery *clientEventDelivery) deliver(raw json.RawMessage) (err error) {
	defer recoverSourceError(&err)
	var event struct {
		Type    string          `json:"type"`
		RunId   *string         `json:"runId"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if event.Type == "message_end" && event.RunId != nil {
		var message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(event.Message, &message); err != nil {
			return err
		}
		if message.Role == "assistant" {
			var content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(message.Content, &content); err != nil {
				return err
			}
			var text strings.Builder
			for _, part := range content {
				if part.Type == "text" {
					text.WriteString(part.Text)
				}
			}
			delivery.completedText[*event.RunId] = text.String()
		}
	}
	if delivery.onEvent != nil {
		return delivery.onEvent(context.Background(), raw)
	}
	return nil
}
