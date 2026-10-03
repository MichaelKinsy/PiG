package experimental

// Ports packages/coding-agent/src/experimental/client.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

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

// RunClientOptions selects discovery. Directory defaults to PI_SERVER_DIR or ~/.pi/server.
type RunClientOptions struct {
	Directory *string
}

// RunClient discovers servers, then lists, attaches or creates a Session and runs a one-shot prompt. ctx owns initial opening; service operations retain upstream's Background Context. A prompt returns the answer's text once AgentController.WaitForPrompt settles.
func RunClient(ctx context.Context, command ClientCommand, options RunClientOptions) (result ClientResult, err error) {
	runtime, err := OpenClientRuntime(ctx, command, OpenClientRuntimeOptions(options))
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
	text, err := promptClientSession(match, *command.Prompt)
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

// promptClientSession starts a prompt and returns its answer's text. The AgentController response and the answer arrive as independent protocol results, so the wait is by operation ID.
func promptClientSession(server *ActivatedClientRuntimeServer, prompt string) (string, error) {
	response, err := server.Agent.Prompt(context.Background(), services.AgentPromptRequest{Message: prompt, Images: nil})
	if err != nil {
		return "", err
	}
	if !response.Accepted {
		if response.Error == nil {
			return "", errors.New("Rejected AgentController operation has no error")
		}
		return "", errors.New(response.Error.Message)
	}
	if response.OperationID == nil {
		return "", errors.New("Accepted AgentController operation has no operation ID")
	}
	result, err := server.Agent.WaitForPrompt(context.Background(), *response.OperationID)
	if err != nil {
		return "", err
	}
	if result.Status == "unanswered" {
		reason := ""
		if result.Reason != nil {
			reason = *result.Reason
		}
		return "", errors.New("Prompt was not answered: " + reason)
	}
	if result.Text == nil {
		return "", errors.New("Answered prompt has no text")
	}
	return *result.Text, nil
}
