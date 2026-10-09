package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// QueuedInputDisposition is how Steer and FollowUp dealt with their input: an input handler consumed it (`handled`) or it was queued (`queued`).
//
// upstream: agent-session.ts:289 (QueuedInputDisposition)
type QueuedInputDisposition string

// PromptDisposition is how Prompt dealt with an accepted input: an extension command or input handler consumed it (`handled`), it was queued behind a running turn (`queued`), or a turn started (`started`).
//
// upstream: agent-session.ts:290 (PromptDisposition)
type PromptDisposition = QueuedInputDisposition

// The dispositions of upstream's PromptDisposition union.
const (
	DispositionHandled QueuedInputDisposition = "handled"
	DispositionQueued  QueuedInputDisposition = "queued"
	DispositionStarted PromptDisposition      = "started"
)

// PromptOptions controls one invocation, independently of the Session's system-prompt construction inputs.
type PromptOptions struct {
	ExpandPromptTemplates *bool
	Images                []ai.ImageContent
	StreamingBehavior     extension.DeliverAs
	Source                extension.InputSource
	// PreflightResult observes how an accepted prompt was dispatched. It is not called when the prompt is rejected.
	//
	// upstream: agent-session.ts:302-303 (PromptOptions.preflightResult)
	PreflightResult func(PromptDisposition)
}

// QueueInputOptions supplies the input source for a direct queue operation.
type QueueInputOptions struct {
	Source extension.InputSource
}

// Steer awaits input handlers and skill/template expansion, rejects extension commands, and queues the result before the next model call.
func (s *Session) Steer(ctx context.Context, text string, images []ai.ImageContent, options *QueueInputOptions) (QueuedInputDisposition, error) {
	return s.queueUserInput(ctx, text, images, extension.DeliverAsSteer, options)
}

// FollowUp awaits input handlers and skill/template expansion, rejects extension commands, and queues the result after the current turn's tools and steering finish.
func (s *Session) FollowUp(ctx context.Context, text string, images []ai.ImageContent, options *QueueInputOptions) (QueuedInputDisposition, error) {
	return s.queueUserInput(ctx, text, images, extension.DeliverAsFollowUp, options)
}

func (s *Session) queueUserInput(ctx context.Context, text string, images []ai.ImageContent, behavior extension.DeliverAs, options *QueueInputOptions) (QueuedInputDisposition, error) {
	if strings.HasPrefix(text, "/") {
		name, _, _ := strings.Cut(text[1:], " ")
		if runner := s.currentRunner(); runner != nil {
			if _, exists := runner.Command(name); exists {
				return "", fmt.Errorf("Extension command %q cannot be queued. Use prompt() or execute the command when not streaming.", "/"+name)
			}
		}
	}
	source := extension.InputSourceUser
	if options != nil && options.Source != "" {
		source = options.Source
	}
	text, images, handled, err := s.RunInputHandlers(ctx, text, images, source, string(behavior))
	if err != nil {
		return "", err
	}
	// upstream: agent-session.ts:2104 (`if (!processedInput) return "handled"`), 2114 (`return "queued"`).
	if handled {
		return DispositionHandled, nil
	}
	text = s.expandPromptText(text)
	if behavior == extension.DeliverAsSteer {
		s.QueueSteer(text, images)
	} else {
		s.QueueFollowUp(text, images)
	}
	return DispositionQueued, nil
}

// GetSystemPromptOptions returns the live base prompt inputs used by command contexts. Mutations apply to this options object, not the Agent's active tool registry. Rebuilding the tool prompt replaces the object; previously borrowed options remain unchanged.
func (s *Session) GetSystemPromptOptions() *extension.BuildSystemPromptOptions {
	if options := s.baseSystemPromptOptions.Load(); options != nil {
		return options
	}
	s.toolRegistryMu.RLock()
	defer s.toolRegistryMu.RUnlock()
	options := s.buildSystemPromptOptions(s.ActiveToolNames())
	if s.baseSystemPromptOptions.CompareAndSwap(nil, options) {
		return options
	}
	return s.baseSystemPromptOptions.Load()
}

// SystemPromptResources is the resource-loader input of Pi's _rebuildSystemPrompt (agent-session.ts:1379-1391): the system prompt override, the joined append text, the loaded context files and the loaded skills.
type SystemPromptResources struct {
	CustomPrompt       string
	CustomPromptSet    bool
	AppendSystemPrompt string
	ContextFiles       []extension.SystemPromptContextFile
	Skills             []extension.SystemPromptSkill
}

// effectiveSystemPromptResources returns the caller's explicit resource state, or the loader's when the Session builds its default prompt from it. It is nil when neither applies.
func (s *Session) effectiveSystemPromptResources() *SystemPromptResources {
	if explicit := s.systemPromptResources.Load(); explicit != nil {
		return explicit
	}
	return s.loaderSystemPromptResources()
}

// loaderSystemPromptResources returns the current skills, context files and prompt text of a Session that builds its default prompt from its loader (agent-session.ts:1379-1391), or nil for any other Session.
func (s *Session) loaderSystemPromptResources() *SystemPromptResources {
	ref := s.resourceLoader.Load()
	if ref == nil || !ref.promptFromLoader || !s.defaultSystemPrompt {
		return nil
	}
	loader := ref.loader
	resources := &SystemPromptResources{Skills: icodingagent.SystemPromptSkills(loader.GetSkills().Skills)}
	resources.CustomPrompt, resources.CustomPromptSet = loader.GetSystemPrompt()
	if appended := loader.GetAppendSystemPrompt(); len(appended) > 0 {
		resources.AppendSystemPrompt = strings.Join(appended, "\n\n")
	}
	for _, file := range loader.GetAgentsFiles().AgentsFiles {
		resources.ContextFiles = append(resources.ContextFiles, extension.SystemPromptContextFile{Path: file.Path, Content: file.Content})
	}
	return resources
}

// buildSystemPromptOptions mirrors _rebuildSystemPrompt (agent-session.ts:1371-1394): registry snippets and guidelines plus the resource-loader state. Without resources, a caller-supplied prompt is the custom prompt, as Pi's system prompt override is.
func (s *Session) buildSystemPromptOptions(toolNames []string) *extension.BuildSystemPromptOptions {
	snippets, guidelines := s.toolPromptMetadata()
	options := &extension.BuildSystemPromptOptions{Cwd: s.CWD(), SelectedTools: append([]string{}, toolNames...), HiddenTools: s.HiddenDeclarationNames(), ToolSnippets: snippets, ToolGuidelines: guidelines}
	if resources := s.effectiveSystemPromptResources(); resources != nil {
		options.CustomPrompt = resources.CustomPrompt
		options.CustomPromptSet = resources.CustomPromptSet
		options.AppendSystemPrompt = resources.AppendSystemPrompt
		options.ContextFiles = slices.Clone(resources.ContextFiles)
		options.Skills = slices.Clone(resources.Skills)
	} else if !s.defaultSystemPrompt {
		if baseline := s.baseSystemPrompt.Load(); baseline != nil {
			options.CustomPrompt = *baseline
		}
	}
	return new(extension.NormalizeBuildSystemPromptOptions(*options))
}

// Prompt awaits command/input dispatch, optional resource expansion, queueing or an ordinary turn. Extension command arguments retain all text after the first space. Invocation options do not replace system-prompt construction state.
func (s *Session) Prompt(ctx context.Context, text string, options ...*PromptOptions) error {
	var opts PromptOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	if s.deferSettledAction(func() { s.reportRuntimeError("prompt", s.Prompt(ctx, text, &opts)) }) {
		return nil
	}
	run, err := s.preparePromptInvocation(ctx, text, opts)
	if err != nil || run == nil {
		return err
	}
	_, err = run.Run()
	return err
}

func (s *Session) preparePromptInvocation(ctx context.Context, text string, options PromptOptions) (*PreparedPromptRun, error) {
	// A rejected prompt reports nothing: upstream calls `preflightResult` only when the prompt is accepted (agent-session.ts:1894-1962).
	accepted := false
	notify := func(disposition PromptDisposition) {
		if options.PreflightResult != nil && !accepted {
			options.PreflightResult(disposition)
		}
		accepted = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expand := options.ExpandPromptTemplates == nil || *options.ExpandPromptTemplates
	if expand && strings.HasPrefix(text, "/") {
		name, args, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
		if runner := s.currentRunner(); runner != nil && runner.ExecuteCommand(ctx, name, args) {
			notify(DispositionHandled)
			return nil, nil
		}
	}
	if s.isManualCompacting() {
		return nil, errPromptDuringCompaction
	}
	source := options.Source
	if source == "" {
		source = extension.InputSourceUser
	}
	text, images, handled, err := s.RunInputHandlers(ctx, text, options.Images, source, string(options.StreamingBehavior))
	if err != nil {
		return nil, err
	}
	if handled {
		notify(DispositionHandled)
		return nil, nil
	}
	if expand {
		text = s.expandPromptText(text)
	}
	if s.IsStreaming() {
		switch options.StreamingBehavior {
		case extension.DeliverAsSteer:
			s.QueueSteer(text, images)
		case extension.DeliverAsFollowUp:
			s.QueueFollowUp(text, images)
		default:
			return nil, errAgentAlreadyProcessing
		}
		notify(DispositionQueued)
		return nil, nil
	}
	if err := s.ValidatePromptModelAuth(ctx); err != nil {
		return nil, err
	}
	return s.prepareContentRun(ctx, BuildUserContent(text, images), func() { notify(DispositionStarted) })
}

// ValidatePromptModelAuth rejects a new prompt before compaction and before_agent_start when no model is selected or its provider has no usable auth, as agent-session.ts:1673-1691 does. Interactive mode runs the same check before it starts a turn.
func (s *Session) ValidatePromptModelAuth(ctx context.Context) error {
	model := s.Model()
	if model == nil {
		return errors.New(icodingagent.FormatNoModelSelectedMessage())
	}
	provider := providerID(model)
	if (s.services.Registry().GetProvider(provider) != nil || modelRuntimeRequiresAuth(provider)) && !s.services.Registry().ModelRegistry.HasConfiguredAuth(provider) {
		check, err := s.modelRuntime.CheckAuth(ctx, provider)
		if err != nil {
			return err
		}
		if check == nil {
			if s.modelRuntime.IsUsingOAuth(provider) {
				return errors.New(`Authentication failed for "` + provider + `". Credentials may have expired or network is unavailable. Run '/login ` + provider + `' to re-authenticate.`)
			}
			return errors.New(icodingagent.FormatNoAPIKeyFoundMessage(provider))
		}
	}
	return nil
}

// SendUserMessage awaits the Session's extension-originated user turn or queue operation. Expansion defaults to false.
func (s *Session) SendUserMessage(ctx context.Context, content any, options *extension.SendUserMessageOptions) error {
	text, images, err := extensionUserMessageContent(content)
	if err != nil {
		return err
	}
	opts := PromptOptions{Source: extension.InputSourceExtension, Images: images, ExpandPromptTemplates: new(false)}
	if options != nil {
		opts.StreamingBehavior = options.DeliverAs
		if options.ExpandPromptTemplates != nil {
			opts.ExpandPromptTemplates = options.ExpandPromptTemplates
		}
	}
	return s.Prompt(ctx, text, &opts)
}

// SendExtensionUserMessage is the bound extension action. The Session owns its continuation and reports failures without making the extension await the model turn.
func (s *Session) SendExtensionUserMessage(content any, options *extension.SendUserMessageOptions) error {
	text, images, err := extensionUserMessageContent(content)
	if err != nil {
		return err
	}
	opts := PromptOptions{Source: extension.InputSourceExtension, Images: images, ExpandPromptTemplates: new(false)}
	if options != nil {
		opts.StreamingBehavior = options.DeliverAs
		if options.ExpandPromptTemplates != nil {
			opts.ExpandPromptTemplates = options.ExpandPromptTemplates
		}
	}
	ctx := s.backgroundContext()
	if s.deferSettledAction(func() { s.reportRuntimeError("send_user_message", s.Prompt(ctx, text, &opts)) }) {
		return nil
	}
	run, err := s.preparePromptInvocation(ctx, text, opts)
	if err != nil || run == nil {
		s.reportRuntimeError("send_user_message", err)
		return nil
	}
	started := make(chan struct{})
	if err := s.startExtensionTask(func() {
		<-started
		_, err := run.Run()
		s.reportRuntimeError("send_user_message", ignoreCancellation(err))
	}); err != nil {
		_, _ = run.Run()
		return err
	}
	_ = run.Start()
	close(started)
	return nil
}

func (s *Session) deferSettledAction(action func()) bool {
	s.runState.mu.Lock()
	defer s.runState.mu.Unlock()
	if !s.runState.settling.Load() {
		return false
	}
	s.runState.deferred = append(s.runState.deferred, action)
	return true
}

// SetSystemPromptResources replaces the resource-loader state behind the base prompt options, as a resource reload does in Pi.
func (s *Session) SetSystemPromptResources(resources SystemPromptResources) {
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	s.systemPromptResources.Store(&resources)
	s.baseSystemPromptOptions.Store(s.buildSystemPromptOptions(s.ActiveToolNames()))
}
