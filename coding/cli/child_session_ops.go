// pig-specific: serves the AgentSession members an extension reaches over the wire (CallChildSessionOp) to the Go, Python and Rust extension SDKs.

package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// opArgs decodes the argument object of a member into T; no argument is T's zero value.
func opArgs[T any](args json.RawMessage) (T, error) {
	var value T
	if len(args) == 0 || string(args) == "null" {
		return value, nil
	}
	if err := json.Unmarshal(args, &value); err != nil {
		return value, fmt.Errorf("decode arguments: %w", err)
	}
	return value, nil
}

// opScopedModel is one entry of scopedModels on the wire: a Pi Model object and the optional thinking level.
type opScopedModel struct {
	Model         json.RawMessage `json:"model"`
	ThinkingLevel string          `json:"thinkingLevel,omitempty"`
}

// opModelEntry is a model and a thinking level as the wire carries them.
type opModelEntry struct {
	Model         extension.ModelValue `json:"model"`
	ThinkingLevel string               `json:"thinkingLevel,omitempty"`
}

func opMarshal(value any) (json.RawMessage, error) { return json.Marshal(value) }

// Op is the AgentSession member named op, run on the session with the argument object args; the members and their shapes are listed at subprocess.ChildSessionOp.
func (c *childSession) Op(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	s := c.session
	switch op {
	case "isStreaming":
		return opMarshal(s.IsStreaming())
	case "isIdle":
		return opMarshal(s.IsIdle())
	case "isCompacting":
		return opMarshal(s.IsCompacting())
	case "isRetrying":
		return opMarshal(s.IsRetrying())
	case "isBashRunning":
		return opMarshal(s.IsBashRunning())
	case "hasPendingBashMessages":
		return opMarshal(s.HasPendingBashMessages())
	case "autoCompactionEnabled":
		return opMarshal(s.AutoCompactionEnabled())
	case "autoRetryEnabled":
		return opMarshal(s.AutoRetryEnabled())
	case "supportsThinking":
		return opMarshal(s.SupportsThinking())
	case "retryAttempt":
		return opMarshal(s.RetryAttempt())
	case "pendingMessageCount":
		return opMarshal(s.PendingMessageCount())
	case "sessionId":
		return opMarshal(s.ID())
	case "systemPrompt":
		return opMarshal(s.SystemPrompt())
	case "thinkingLevel":
		return opMarshal(string(s.ThinkingLevel()))
	case "steeringMode":
		return opMarshal(string(s.SteeringMode()))
	case "followUpMode":
		return opMarshal(string(s.FollowUpMode()))
	case "sessionFile":
		if file := s.Inner().GetSessionFile(); file != nil {
			return opMarshal(*file)
		}
		return opMarshal(nil)
	case "sessionName":
		if name := s.SessionName(); name != "" {
			return opMarshal(name)
		}
		return opMarshal(nil)
	case "getLastAssistantText":
		return opMarshal(s.LastAssistantText())
	case "model":
		if model := s.Model(); model != nil {
			return extension.ModelObject(model)
		}
		return opMarshal(nil)
	case "routedModel":
		routed := s.RoutedModel()
		if routed == nil {
			return opMarshal(nil)
		}
		return opMarshal(opModelEntry{Model: extension.ModelValue{Model: routed.Model}, ThinkingLevel: string(routed.ThinkingLevel)})
	case "scopedModels":
		scoped := s.ScopedModels()
		out := make([]opModelEntry, len(scoped))
		for i, entry := range scoped {
			out[i] = opModelEntry{Model: extension.ModelValue{Model: entry.Model}, ThinkingLevel: string(entry.ThinkingLevel)}
		}
		return opMarshal(out)
	case "getActiveToolNames":
		return opMarshal(opList(s.ActiveToolNames()))
	case "getCallableToolNames":
		return opMarshal(opList(s.CallableToolNames()))
	case "getSteeringMessages":
		return opMarshal(opList(s.GetSteeringMessages()))
	case "getFollowUpMessages":
		return opMarshal(opList(s.GetFollowUpMessages()))
	case "getAvailableThinkingLevels":
		levels := s.AvailableThinkingLevels()
		out := make([]string, len(levels))
		for i, level := range levels {
			out[i] = string(level)
		}
		return opMarshal(out)
	case "getAllTools":
		all := s.GetAllTools()
		out := make([]opToolInfo, len(all))
		for i, info := range all {
			out[i] = opToolInfoOf(info)
		}
		return opMarshal(out)
	case "getToolDefinition":
		p, err := opArgs[struct {
			Name string `json:"name"`
		}](args)
		if err != nil {
			return nil, err
		}
		definition, ok := s.GetToolDefinition(p.Name)
		if !ok {
			return opMarshal(nil)
		}
		for _, info := range s.GetAllTools() {
			if info.Name == definition.Name {
				return opMarshal(opToolInfoOf(info))
			}
		}
		return opMarshal(nil)
	case "getContextUsage":
		return opMarshal(s.ContextUsage())
	case "getSessionStats":
		return opMarshal(s.GetSessionStats())
	case "getUserMessagesForForking":
		return opMarshal(opList(s.UserMessagesForForking()))
	case "promptTemplates":
		templates := s.PromptTemplates()
		out := make([]opPromptTemplate, len(templates))
		for i, template := range templates {
			out[i] = opPromptTemplate{Name: template.Name, Description: template.Description, ArgumentHint: template.ArgumentHint, Content: template.Content, FilePath: template.FilePath, SourceInfo: template.SourceInfo}
		}
		return opMarshal(out)
	case "cacheWarmingStatus":
		return opMarshal(s.CacheWarmingStatus())
	case "hasExtensionHandlers":
		p, err := opArgs[struct {
			EventType string `json:"eventType"`
		}](args)
		if err != nil {
			return nil, err
		}
		return opMarshal(s.HasExtensionHandlers(p.EventType))
	case "setModel":
		p, err := opArgs[struct {
			Model   json.RawMessage `json:"model"`
			Persist bool            `json:"persist"`
		}](args)
		if err != nil {
			return nil, err
		}
		model, err := resolveChildModel(s.ModelRuntime(), p.Model)
		if err != nil {
			return nil, err
		}
		return nil, s.SetModel(model, coding.ModelMutationOptions{Persist: p.Persist})
	case "cycleModel":
		p, err := opArgs[struct {
			Direction string `json:"direction"`
			Persist   bool   `json:"persist"`
		}](args)
		if err != nil {
			return nil, err
		}
		if p.Direction == "" {
			p.Direction = "forward"
		}
		result, err := s.CycleModel(p.Direction, coding.ModelMutationOptions{Persist: p.Persist})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return opMarshal(nil)
		}
		return opMarshal(struct {
			Model         extension.ModelValue `json:"model"`
			ThinkingLevel string               `json:"thinkingLevel"`
			IsScoped      bool                 `json:"isScoped"`
		}{extension.ModelValue{Model: result.Model}, string(result.ThinkingLevel), result.IsScoped})
	case "setThinkingLevel":
		p, err := opArgs[struct {
			Level   string `json:"level"`
			Persist bool   `json:"persist"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.SetThinkingLevel(ai.ModelThinkingLevel(p.Level), coding.ModelMutationOptions{Persist: p.Persist})
	case "cycleThinkingLevel":
		p, err := opArgs[struct {
			Persist bool `json:"persist"`
		}](args)
		if err != nil {
			return nil, err
		}
		level, err := s.CycleThinkingLevel(coding.ModelMutationOptions{Persist: p.Persist})
		if err != nil {
			return nil, err
		}
		if level == "" {
			return opMarshal(nil)
		}
		return opMarshal(string(level))
	case "setScopedModels":
		p, err := opArgs[struct {
			Models []opScopedModel `json:"models"`
		}](args)
		if err != nil {
			return nil, err
		}
		models := make([]coding.ScopedModel, len(p.Models))
		for i, entry := range p.Models {
			model, err := resolveChildModel(s.ModelRuntime(), entry.Model)
			if err != nil {
				return nil, err
			}
			models[i] = coding.ScopedModel{Model: model, ThinkingLevel: agent.ThinkingLevel(entry.ThinkingLevel)}
		}
		s.SetScopedModels(models)
		return nil, nil
	case "setActiveToolsByName":
		p, err := opArgs[struct {
			Names []string `json:"names"`
		}](args)
		if err != nil {
			return nil, err
		}
		s.SetActiveToolsByName(p.Names)
		return nil, nil
	case "setSteeringMode":
		p, err := opArgs[struct {
			Mode string `json:"mode"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.SetSteeringMode(agent.QueueMode(p.Mode))
	case "setFollowUpMode":
		p, err := opArgs[struct {
			Mode string `json:"mode"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.SetFollowUpMode(agent.QueueMode(p.Mode))
	case "setAutoCompactionEnabled":
		p, err := opArgs[struct {
			Enabled bool `json:"enabled"`
		}](args)
		if err != nil {
			return nil, err
		}
		s.SetAutoCompactionEnabled(p.Enabled)
		return nil, nil
	case "setAutoRetryEnabled":
		p, err := opArgs[struct {
			Enabled bool `json:"enabled"`
		}](args)
		if err != nil {
			return nil, err
		}
		s.SetAutoRetryEnabled(p.Enabled)
		return nil, nil
	case "setCacheWarmingMode":
		p, err := opArgs[struct {
			Mode string `json:"mode"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.SetCacheWarmingMode(icodingagent.CacheWarmingMode(p.Mode))
	case "setSessionName":
		p, err := opArgs[struct {
			Name string `json:"name"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.SetSessionName(p.Name)
	case "clearQueue":
		steering, followUp := s.ClearQueue()
		return opMarshal(struct {
			Steering []string `json:"steering"`
			FollowUp []string `json:"followUp"`
		}{opList(steering), opList(followUp)})
	case "abortBash":
		s.AbortBash()
		return nil, nil
	case "abortBranchSummary":
		s.AbortBranchSummary()
		return nil, nil
	case "abortCompaction":
		s.AbortCompaction()
		return nil, nil
	case "abortRetry":
		s.AbortRetry()
		return nil, nil
	case "waitForIdle":
		return nil, s.WaitForIdle(ctx)
	case "reload":
		return nil, s.Reload(ctx)
	case "compact":
		p, err := opArgs[struct {
			CustomInstructions string `json:"customInstructions"`
		}](args)
		if err != nil {
			return nil, err
		}
		result, err := s.CompactForExtension(ctx, p.CustomInstructions)
		if err != nil {
			return nil, err
		}
		return opMarshal(result)
	case "navigateTree":
		p, err := opArgs[struct {
			TargetID            string `json:"targetId"`
			Summarize           bool   `json:"summarize"`
			CustomInstructions  string `json:"customInstructions"`
			ReplaceInstructions bool   `json:"replaceInstructions"`
			Label               string `json:"label"`
		}](args)
		if err != nil {
			return nil, err
		}
		result, err := s.NavigateTree(ctx, p.TargetID, coding.NavigateTreeOptions{Summarize: p.Summarize, CustomInstructions: p.CustomInstructions, ReplaceInstructions: p.ReplaceInstructions, Label: p.Label})
		if err != nil {
			return nil, err
		}
		return opMarshal(struct {
			Cancelled    bool                       `json:"cancelled"`
			Aborted      bool                       `json:"aborted,omitempty"`
			EditorText   string                     `json:"editorText,omitempty"`
			SummaryEntry *coding.BranchSummaryEntry `json:"summaryEntry,omitempty"`
		}{result.Cancelled, result.Aborted, result.EditorText, result.SummaryEntry})
	case "executeBash":
		p, err := opArgs[struct {
			Command            string `json:"command"`
			ExcludeFromContext *bool  `json:"excludeFromContext"`
		}](args)
		if err != nil {
			return nil, err
		}
		result, err := s.ExecuteBash(ctx, p.Command, nil, &coding.ExecuteBashOptions{ExcludeFromContext: p.ExcludeFromContext})
		if err != nil {
			return nil, err
		}
		return opMarshal(result)
	case "recordBashResult":
		p, err := opArgs[struct {
			Command            string            `json:"command"`
			Result             coding.BashResult `json:"result"`
			ExcludeFromContext *bool             `json:"excludeFromContext"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.RecordBashResult(p.Command, p.Result, p.ExcludeFromContext)
	case "exportToHtml":
		p, err := opArgs[struct {
			OutputPath string `json:"outputPath"`
			ThemeName  string `json:"themeName"`
		}](args)
		if err != nil {
			return nil, err
		}
		path, err := s.ExportToHTML(p.OutputPath, coding.ExportToHTMLOptions{ThemeName: p.ThemeName})
		if err != nil {
			return nil, err
		}
		return opMarshal(path)
	case "exportToJsonl":
		p, err := opArgs[struct {
			OutputPath string `json:"outputPath"`
		}](args)
		if err != nil {
			return nil, err
		}
		path, err := s.ExportToJsonl(p.OutputPath)
		if err != nil {
			return nil, err
		}
		return opMarshal(path)
	case "summarizeForBugReport":
		p, err := opArgs[struct {
			Hint string `json:"hint"`
		}](args)
		if err != nil {
			return nil, err
		}
		text, err := s.SummarizeForBugReport(ctx, p.Hint)
		if err != nil {
			return nil, err
		}
		return opMarshal(text)
	case "sendCustomMessage":
		p, err := opArgs[struct {
			Message extension.CustomMessageRef    `json:"message"`
			Options *extension.SendMessageOptions `json:"options"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.SendCustomMessage(ctx, p.Message, p.Options)
	case "sendUserMessage":
		p, err := opArgs[struct {
			Content any                               `json:"content"`
			Options *extension.SendUserMessageOptions `json:"options"`
		}](args)
		if err != nil {
			return nil, err
		}
		return nil, s.SendUserMessage(ctx, p.Content, p.Options)
	}
	return nil, errors.New(subprocess.ChildSessionUnknownOpMessage + op)
}

// opList is the empty list for a nil one: Pi's lists are never undefined.
func opList[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

// opToolInfo is a tool as getAllTools lists it: the wire ToolInfo and the `source` field the SDKs released before sourceInfo read. The definition's reachesTools hook is a function of the extension that registered the tool and does not cross the wire.
type opToolInfo struct {
	subprocess.ToolInfo
	Source string `json:"source,omitempty"`
}

func opToolInfoOf(info extension.ToolInfo) opToolInfo {
	return opToolInfo{
		ToolInfo: subprocess.ToolInfo{
			Name: info.Name, Description: info.Description, Parameters: info.Parameters, PromptGuidelines: info.PromptGuidelines,
			Exposure: info.Exposure, Namespace: info.Namespace, Annotations: info.Annotations, SourceInfo: info.SourceInfo,
		},
		Source: info.Source,
	}
}

// opPromptTemplate is Pi's PromptTemplate on the wire.
type opPromptTemplate struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint,omitempty"`
	Content      string `json:"content"`
	FilePath     string `json:"filePath"`
	SourceInfo   any    `json:"sourceInfo"`
}
