// pig-specific: serves createAgentSession (packages/coding-agent/src/core/sdk.ts) to the Go, Python and Rust extension SDKs.

package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// BindChildSessions lets the extensions of bridge's host create child agent sessions. A child shares the model runtime of services (Pi's `modelRuntime: ctx.modelRegistry.runtime`), so every provider the extensions registered is available to it, and its tools, settings and resources are its own.
func BindChildSessions(bridge *subprocess.UIBridge, services *coding.AgentSessionServices) {
	if bridge == nil || services == nil {
		return
	}
	bridge.SetHostAction("createChildSession", ChildSessionFactory(services))
}

// ChildSessionFactory is the factory BindChildSessions installs: it builds each session over services' model runtime.
func ChildSessionFactory(services *coding.AgentSessionServices) subprocess.ChildSessionFactory {
	return func(ctx context.Context, request subprocess.ChildSessionRequest) (subprocess.ChildSession, error) {
		return newChildSession(ctx, services, request)
	}
}

// childSession is a coding.Session behind the extension wire.
type childSession struct {
	session *coding.Session
	// services are the child's own: the session's settings, resource loader and extension host, over the parent's model runtime. Dispose closes them.
	services *coding.AgentSessionServices
	info     subprocess.ChildSessionInfo
	disposed sync.Once
}

// newChildSession is sdk.ts createAgentSession over the parent's model runtime.
func newChildSession(ctx context.Context, parent *coding.AgentSessionServices, request subprocess.ChildSessionRequest) (subprocess.ChildSession, error) {
	runtime := parent.ModelRuntime()
	if request.ModelRuntime != nil {
		shared, ok := request.ModelRuntime.(*coding.GraphModelRuntime)
		if !ok {
			return nil, errors.New("option modelRuntime does not name a ModelRuntime")
		}
		runtime = shared.Runtime()
	}
	options := coding.CreateAgentSessionServicesOptions{CWD: request.Cwd, AgentDir: request.AgentDir, ModelRuntime: runtime, ModelRuntimeSignal: ctx}
	if options.CWD == "" {
		options.CWD = parent.CWD()
	}
	if options.AgentDir == "" {
		options.AgentDir = parent.AgentDir()
	}
	// upstream: sdk.ts:197-199 `options.settingsManager ?? SettingsManager.create(cwd, agentDir)`; an extension that passes `SettingsManager.inMemory(settings)` reads no settings file.
	if len(request.Settings) != 0 {
		var settings coding.Settings
		if err := json.Unmarshal(request.Settings, &settings); err != nil {
			return nil, fmt.Errorf("Invalid settings option: %w", err)
		}
		options.SettingsManager = coding.NewInMemorySettingsManager(settings)
	}
	if request.SettingsManager != nil {
		shared, ok := request.SettingsManager.(*icodingagent.GraphSettingsManager)
		if !ok {
			return nil, errors.New("option settingsManager does not name a SettingsManager")
		}
		options.SettingsManager = shared.Manager()
	}
	if request.SessionManager != nil {
		shared, ok := request.SessionManager.(*icodingagent.GraphSessionManager)
		if !ok {
			return nil, errors.New("option sessionManager does not name a SessionManager")
		}
		if request.Cwd == "" {
			options.CWD = shared.Session().CWD()
		}
	}
	if request.ResourceLoader != nil {
		shared, ok := request.ResourceLoader.(*coding.GraphResourceLoader)
		if !ok {
			return nil, errors.New("option resourceLoader does not name a DefaultResourceLoader")
		}
		options.ResourceLoader = shared.Loader()
	} else if request.Resources != nil {
		options.ResourceLoaderOptions = childResourceLoaderOptions(request.Resources)
	}
	services, err := coding.CreateAgentSessionServices(options)
	if err != nil {
		return nil, err
	}
	child, err := createChildSession(ctx, services, runtime, request)
	if err != nil {
		services.Close()
		return nil, err
	}
	child.services = services
	return child, nil
}

func createChildSession(ctx context.Context, services *coding.AgentSessionServices, runtime *coding.ModelRuntime, request subprocess.ChildSessionRequest) (*childSession, error) {
	create := coding.CreateAgentSessionFromServicesOptions{
		Services:      services,
		ThinkingLevel: ai.ModelThinkingLevel(request.ThinkingLevel),
		ExcludeTools:  request.ExcludeTools,
		NoTools:       request.NoTools,
		CustomTools:   request.ToolDefinitions,
	}
	if request.Tools != nil {
		create.Tools = slices.Clone(*request.Tools)
		if create.Tools == nil {
			create.Tools = []string{}
		}
	}
	if request.SessionManager != nil {
		create.SessionManager = request.SessionManager.(*icodingagent.GraphSessionManager).Session()
	} else if request.InMemory {
		manager, err := coding.NewInMemorySessionManager(services.CWD())
		if err != nil {
			return nil, err
		}
		create.SessionManager = manager
	}
	if len(request.Model) != 0 {
		model, err := resolveChildModel(runtime, request.Model)
		if err != nil {
			return nil, err
		}
		create.Model = model
	}
	for _, scoped := range request.ScopedModels {
		model, err := resolveChildModel(runtime, scoped.Model)
		if err != nil {
			return nil, fmt.Errorf("option scopedModels: %w", err)
		}
		create.ScopedModels = append(create.ScopedModels, coding.ScopedModel{Model: model, ThinkingLevel: agent.ThinkingLevel(scoped.ThinkingLevel)})
	}
	result, err := coding.CreateAgentSessionFromServices(create)
	if err != nil {
		return nil, err
	}
	session := result.Session
	// The wire's listeners are the only sinks: nothing reads Events.
	session.DiscardEvents()
	child := &childSession{session: session}
	child.info = child.snapshotInfo()
	child.info.ModelFallbackMessage = result.ModelFallbackMessage
	for _, loadErr := range result.ExtensionsResult.Errors {
		child.info.ExtensionErrors = append(child.info.ExtensionErrors, loadErr.Path+": "+loadErr.Error)
	}
	return child, nil
}

// childResourceLoaderOptions maps the wire's resource selection to DefaultResourceLoader options (resource-loader.ts).
func childResourceLoaderOptions(resources *subprocess.ChildSessionResources) *coding.DefaultResourceLoaderOptions {
	options := &coding.DefaultResourceLoaderOptions{
		NoExtensions:                  resources.NoExtensions,
		NoSkills:                      resources.NoSkills,
		NoPromptTemplates:             resources.NoPromptTemplates,
		NoThemes:                      resources.NoThemes,
		NoContextFiles:                resources.NoContextFiles,
		AdditionalExtensionPaths:      resources.AdditionalExtensionPaths,
		AdditionalSkillPaths:          resources.AdditionalSkillPaths,
		AdditionalPromptTemplatePaths: resources.AdditionalPromptTemplatePaths,
		SystemPrompt:                  resources.SystemPrompt,
	}
	if resources.AppendSystemPrompt != nil {
		options.AppendSystemPrompt = slices.Clone(*resources.AppendSystemPrompt)
		if options.AppendSystemPrompt == nil {
			options.AppendSystemPrompt = []string{}
		}
	}
	if resources.ExtensionNames != nil {
		names := slices.Clone(*resources.ExtensionNames)
		// An extension is kept when its path contains one of the names, compared without case.
		options.ExtensionsOverride = func(result coding.LoadExtensionsResult) coding.LoadExtensionsResult {
			result.Extensions = slices.DeleteFunc(slices.Clone(result.Extensions), func(ext extension.Extension) bool {
				path := strings.ToLower(ext.Path)
				return !slices.ContainsFunc(names, func(name string) bool { return strings.Contains(path, strings.ToLower(name)) })
			})
			return result
		}
	}
	if resources.SkillNames != nil {
		names := slices.Clone(*resources.SkillNames)
		// A skill is kept when its name is one of the names.
		options.SkillsOverride = func(result coding.SkillsResult) coding.SkillsResult {
			result.Skills = slices.DeleteFunc(slices.Clone(result.Skills), func(skill *coding.Skill) bool { return !slices.Contains(names, skill.Name) })
			return result
		}
	}
	return options
}

// resolveChildModel resolves the `model` option as Pi's takes it: a model, which crosses the wire as the object naming it by provider and id. The caller resolves a name or pattern through the registry before it asks. A model the host's registry does not hold is an error where Pi would use the object as given, because a wire object carries no stream function.
func resolveChildModel(runtime *coding.ModelRuntime, raw json.RawMessage) (*ai.Model, error) {
	var object struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.Provider == "" || object.ID == "" {
		return nil, errors.New("Invalid model option: want a model, an object with provider and id")
	}
	model := runtime.GetModel(object.Provider, object.ID)
	if model == nil {
		return nil, fmt.Errorf("Model %s/%s not found", object.Provider, object.ID)
	}
	return model, nil
}

func (c *childSession) Info() subprocess.ChildSessionInfo { return c.info }

// snapshotInfo is the session's state as createAgentSession's caller first reads it.
func (c *childSession) snapshotInfo() subprocess.ChildSessionInfo {
	state := c.State()
	return subprocess.ChildSessionInfo{
		SessionID: state.SessionID, SessionFile: state.SessionFile, Model: state.Model,
		ThinkingLevel: state.ThinkingLevel, ActiveTools: state.ActiveTools, SystemPrompt: state.SystemPrompt,
	}
}

func (c *childSession) State() subprocess.ChildSessionState {
	session := c.session
	state := subprocess.ChildSessionState{
		SessionID:           session.ID(),
		IsStreaming:         session.IsStreaming(),
		IsIdle:              session.IsIdle(),
		ThinkingLevel:       string(session.ThinkingLevel()),
		ActiveTools:         session.ActiveToolNames(),
		SystemPrompt:        session.SystemPrompt(),
		PendingMessageCount: session.PendingMessageCount(),
		LastAssistantText:   session.LastAssistantText(),
		Model:               json.RawMessage("null"),
	}
	if state.ActiveTools == nil {
		state.ActiveTools = []string{}
	}
	if file := session.Inner().GetSessionFile(); file != nil {
		state.SessionFile = *file
	}
	if model := session.Model(); model != nil {
		if object, err := extension.ModelObject(model); err == nil {
			state.Model = object
		}
	}
	return state
}

func (c *childSession) Prompt(ctx context.Context, prompt subprocess.ChildSessionPrompt) error {
	options := &coding.PromptOptions{
		ExpandPromptTemplates: prompt.ExpandPromptTemplates,
		StreamingBehavior:     extension.DeliverAs(prompt.StreamingBehavior),
		Source:                extension.InputSource(prompt.Source),
		Images:                childImages(prompt.Images),
	}
	if err := c.session.Prompt(ctx, prompt.Text, options); err != nil {
		return err
	}
	// Every listener has run through the run's last event before the call returns, as Pi's emission is synchronous.
	return c.session.FlushEvents(ctx)
}

func (c *childSession) Queue(ctx context.Context, queue subprocess.ChildSessionQueue) error {
	images := childImages(queue.Images)
	var err error
	if queue.Behavior == string(extension.DeliverAsFollowUp) {
		_, err = c.session.FollowUp(ctx, queue.Text, images, nil)
	} else {
		_, err = c.session.Steer(ctx, queue.Text, images, nil)
	}
	return err
}

func childImages(images []subprocess.ChildSessionImage) []ai.ImageContent {
	if len(images) == 0 {
		return nil
	}
	out := make([]ai.ImageContent, len(images))
	for i, image := range images {
		out[i] = ai.ImageContent{Data: image.Data, MimeType: image.MimeType}
	}
	return out
}

func (c *childSession) Abort(ctx context.Context) error { return c.session.Abort(ctx) }

func (c *childSession) BindExtensions(ctx context.Context) error {
	// The session runs headless: no UI, no command context actions.
	return c.session.BindExtensions(ctx, coding.ExtensionBindings{})
}

// Messages is AgentSession.messages, in the shape RPC get_messages writes (the leading system message included, as the session's events carry it).
func (c *childSession) Messages() (json.RawMessage, error) {
	wire, err := rpcAgentMessages(c.session.Messages())
	if err != nil {
		return nil, err
	}
	return jsstring.AppendMarshalJSON(nil, wire)
}

// eventBuffers pool the encoding of one event.
var childEventBuffers = sync.Pool{New: func() any { return new([]byte) }}

func (c *childSession) Subscribe(listener func(event json.RawMessage)) func() {
	return c.session.Subscribe(func(event agent.AgentEvent) {
		buffer := childEventBuffers.Get().(*[]byte)
		defer func() {
			if cap(*buffer) > maxPooledJSONLine {
				*buffer = nil
			}
			childEventBuffers.Put(buffer)
		}()
		if update, ok := event.(agent.MessageUpdateEvent); ok {
			// AgentSession.subscribe hands a listener the message so far and the assistant event with its partial, as extension handlers get them; RPC mode's message_update drops both.
			encoded, err := jsstring.AppendMarshalJSON((*buffer)[:0], extension.MessageUpdateEvent{Type: "message_update", Message: update.Message, AssistantMessageEvent: update.AssistantMessageEvent})
			if err != nil {
				return
			}
			*buffer = encoded
			listener(encoded)
			return
		}
		frames, err := rpcAgentEvent(event)
		if err != nil {
			// The session's own conversion failure: the event cannot cross the wire, and the run goes on.
			return
		}
		for _, frame := range frames {
			encoded, err := jsstring.AppendMarshalJSON((*buffer)[:0], frame)
			if err != nil {
				continue
			}
			*buffer = encoded
			listener(encoded)
		}
	})
}

// Dispose is AgentSession.dispose: the session is disconnected from its agent and a later prompt still runs it.
func (c *childSession) Dispose() { c.session.Dispose() }

// Close ends the run and releases the session and the services the child started.
func (c *childSession) Close() {
	c.disposed.Do(func() {
		c.session.Dispose()
		_ = c.session.Close()
		if c.services != nil {
			c.services.Close()
		}
	})
}
