package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func main() {
	root, err := os.MkdirTemp("", "dynamic-provider-")
	must(err)
	defer func() { must(os.RemoveAll(root)) }()
	must(os.Setenv("ANTHROPIC_API_KEY", "test-key"))
	var rows [][]string
	for _, phase := range []string{"top-level", "session-start", "command", "native-top-level", "native-command"} {
		dir, err := os.MkdirTemp(root, "case-")
		must(err)
		services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		must(err)
		model, err := coding.BuildModel("anthropic/claude-sonnet-4-5", services)
		must(err)
		url := "http://localhost:8080/" + phase
		register := func() {
			if strings.HasPrefix(phase, "native-") {
				catalog, _ := ai.LookupModelExact("anthropic/claude-sonnet-4-5")
				m := catalog.ToModel()
				m.ProviderMeta.BaseURL = url
				unused := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					panic("unused")
				}
				must(services.Registry().RegisterNativeModelsProvider(&ai.ModelsProvider{ID: "anthropic", Name: "Native Anthropic", BaseURL: url, GetModels: func() ([]*ai.Model, error) { return []*ai.Model{m}, nil }, Stream: unused, StreamSimple: unused, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Test API key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
					return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "test-key"}, Source: "test"}, nil
				}}}}))
			} else {
				must(services.Registry().RegisterExtensionProvider("anthropic", extension.ProviderConfig{BaseURL: url}))
			}
		}
		ext := extension.Extension{Path: "dynamic"}
		if strings.HasSuffix(phase, "top-level") {
			register()
		}
		if phase == "session-start" {
			ext.Handlers = map[string][]extension.HandlerFn{"session_start": {func(...any) (any, error) { register(); return nil, nil }}}
		}
		ext.Commands = map[string]extension.RegisteredCommand{"use-proxy": {Name: "use-proxy", Description: "Use proxy", Handler: func(context.Context, string) error { register(); return nil }}}
		session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: inproc.NewRunner([]extension.Extension{ext}, dir), NoSession: true, SkipBuiltinTools: true})
		must(err)
		done := make(chan struct{})
		go func() {
			for range session.Events() {
			}
			close(done)
		}()
		if phase == "session-start" {
			session.EmitSessionStart("startup")
		}
		if strings.HasSuffix(phase, "command") {
			_, err := session.Send(context.Background(), "/use-proxy")
			must(err)
		}
		active := session.Model().ProviderMeta.BaseURL
		var sent string
		session.Agent().SetStreamFunction(func(_ context.Context, m *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			sent = m.ProviderMeta.BaseURL
			return nil, errors.New("stop")
		})
		_, err = session.Send(context.Background(), "hello")
		must(err)
		must(session.Close())
		<-done
		rows = append(rows, []string{phase, active, sent})
	}
	must(json.NewEncoder(os.Stdout).Encode(rows))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
