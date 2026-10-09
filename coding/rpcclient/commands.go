package rpcclient

import (
	"bytes"
	"errors"
	"time"
)

// rpcCommand is an RpcCommand body without its id. Fields keep upstream's
// object-literal order; an omitted optional field is upstream's undefined,
// which JSON.stringify drops.
type rpcCommand struct {
	typ    string
	fields []commandField
}

type commandField struct {
	key   string
	value any
}

func command(typ string, fields ...commandField) rpcCommand {
	return rpcCommand{typ: typ, fields: fields}
}

func field(key string, value any) commandField { return commandField{key: key, value: value} }

// optionalField returns the field when value is present, mirroring an
// undefined optional property that JSON.stringify omits.
func optionalField[T any](key string, value *T) []commandField {
	if value == nil {
		return nil
	}
	return []commandField{field(key, *value)}
}

// imagesField mirrors `images: images`: nil is undefined and omitted, while an
// empty slice is sent as [].
func imagesField(images []ImageContent) []commandField {
	if images == nil {
		return nil
	}
	return []commandField{field("images", images)}
}

// marshal serializes {...command, id} as one JSONL record: the command's
// fields in order, then the id, without HTML escaping (JSON.stringify).
func (c rpcCommand) marshal(id string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	fields := append([]commandField{field("type", c.typ)}, c.fields...)
	fields = append(fields, field("id", id))
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := encodeJSON(&buf, f.key); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := encodeJSON(&buf, f.value); err != nil {
			return nil, err
		}
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

func encodeJSON(buf *bytes.Buffer, value any) error {
	line, err := SerializeJsonLine(value)
	if err != nil {
		return err
	}
	buf.Write(line[:len(line)-1])
	return nil
}

func sendFor[T any](c *RpcClient, cmd rpcCommand) (T, error) {
	response, err := c.send(cmd)
	if err != nil {
		var zero T
		return zero, err
	}
	return getData[T](response)
}

func (c *RpcClient) sendOnly(cmd rpcCommand) error {
	_, err := c.send(cmd)
	return err
}

// Prompt sends a prompt and returns its disposition once it is accepted; use
// OnEvent for the streamed events. If the disposition is "handled", no run
// started for this prompt, so do not wait for agent_settled. streamingBehavior
// is nil unless the prompt may arrive during a run.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/src/modes/rpc/rpc-client.ts:198-205 (prompt)
func (c *RpcClient) Prompt(message string, images []ImageContent, streamingBehavior *StreamingBehavior) (PromptDisposition, error) {
	fields := append([]commandField{field("message", message)}, imagesField(images)...)
	fields = append(fields, optionalField("streamingBehavior", streamingBehavior)...)
	disposition, err := c.sendForDisposition(command("prompt", fields...))
	return PromptDisposition(disposition), err
}

// Steer queues a steering message to interrupt the agent mid-run and returns
// its disposition.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/src/modes/rpc/rpc-client.ts:210-213 (steer)
func (c *RpcClient) Steer(message string, images []ImageContent) (QueuedInputDisposition, error) {
	disposition, err := c.sendForDisposition(command("steer", append([]commandField{field("message", message)}, imagesField(images)...)...))
	return QueuedInputDisposition(disposition), err
}

// FollowUp queues a follow-up message for after the agent finishes and returns
// its disposition.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/src/modes/rpc/rpc-client.ts:218-221 (followUp)
func (c *RpcClient) FollowUp(message string, images []ImageContent) (QueuedInputDisposition, error) {
	disposition, err := c.sendForDisposition(command("follow_up", append([]commandField{field("message", message)}, imagesField(images)...)...))
	return QueuedInputDisposition(disposition), err
}

// sendForDisposition sends a prompting command and reads `data.disposition` of
// its response. A success response without a data object cannot be read: upstream's
// `getData(response).disposition` throws a TypeError (rpc-client.ts:203,212,220).
func (c *RpcClient) sendForDisposition(cmd rpcCommand) (string, error) {
	response, err := c.send(cmd)
	if err != nil {
		return "", err
	}
	if response.Success {
		switch string(response.Data) {
		case "":
			return "", errors.New("Cannot read properties of undefined (reading 'disposition')")
		case "null":
			return "", errors.New("Cannot read properties of null (reading 'disposition')")
		}
	}
	data, err := getData[struct {
		Disposition string `json:"disposition"`
	}](response)
	return data.Disposition, err
}

// Abort aborts the current operation.
func (c *RpcClient) Abort() error { return c.sendOnly(command("abort")) }

// ClearQueue clears queued steering and follow-up messages, returning their
// text.
func (c *RpcClient) ClearQueue() (ClearQueueResult, error) {
	return sendFor[ClearQueueResult](c, command("clear_queue"))
}

// NewSession starts a new session, optionally tracking parentSession. The
// result reports whether an extension cancelled it.
func (c *RpcClient) NewSession(parentSession *string) (CancelledResult, error) {
	return sendFor[CancelledResult](c, command("new_session", optionalField("parentSession", parentSession)...))
}

// GetState returns the current session state.
func (c *RpcClient) GetState() (RpcSessionState, error) {
	return sendFor[RpcSessionState](c, command("get_state"))
}

// SetModel sets the model by provider and id.
func (c *RpcClient) SetModel(provider, modelID string) (ModelReference, error) {
	return sendFor[ModelReference](c, command("set_model", field("provider", provider), field("modelId", modelID)))
}

// CycleModel cycles to the next model; nil means there was nothing to cycle.
func (c *RpcClient) CycleModel() (*CycleModelResult, error) {
	return sendFor[*CycleModelResult](c, command("cycle_model"))
}

// GetAvailableModels lists the available models.
func (c *RpcClient) GetAvailableModels() ([]ModelInfo, error) {
	data, err := sendFor[struct {
		Models []ModelInfo `json:"models"`
	}](c, command("get_available_models"))
	return data.Models, err
}

// SetThinkingLevel sets the thinking level.
func (c *RpcClient) SetThinkingLevel(level ThinkingLevel) error {
	return c.sendOnly(command("set_thinking_level", field("level", level)))
}

// CycleThinkingLevel cycles the thinking level; nil means the model has only
// one level.
func (c *RpcClient) CycleThinkingLevel() (*CycleThinkingLevelResult, error) {
	return sendFor[*CycleThinkingLevelResult](c, command("cycle_thinking_level"))
}

// GetAvailableThinkingLevels lists the current model's thinking levels.
func (c *RpcClient) GetAvailableThinkingLevels() ([]ThinkingLevel, error) {
	data, err := sendFor[struct {
		Levels []ThinkingLevel `json:"levels"`
	}](c, command("get_available_thinking_levels"))
	return data.Levels, err
}

// SetSteeringMode sets the steering mode ("all" or "one-at-a-time").
func (c *RpcClient) SetSteeringMode(mode string) error {
	return c.sendOnly(command("set_steering_mode", field("mode", mode)))
}

// SetFollowUpMode sets the follow-up mode ("all" or "one-at-a-time").
func (c *RpcClient) SetFollowUpMode(mode string) error {
	return c.sendOnly(command("set_follow_up_mode", field("mode", mode)))
}

// Compact compacts the session context.
func (c *RpcClient) Compact(customInstructions *string) (CompactionResult, error) {
	return sendFor[CompactionResult](c, command("compact", optionalField("customInstructions", customInstructions)...))
}

// SetAutoCompaction enables or disables auto-compaction.
func (c *RpcClient) SetAutoCompaction(enabled bool) error {
	return c.sendOnly(command("set_auto_compaction", field("enabled", enabled)))
}

// SetAutoRetry enables or disables auto-retry.
func (c *RpcClient) SetAutoRetry(enabled bool) error {
	return c.sendOnly(command("set_auto_retry", field("enabled", enabled)))
}

// AbortRetry aborts an in-progress retry.
func (c *RpcClient) AbortRetry() error { return c.sendOnly(command("abort_retry")) }

// Bash executes a bash command.
func (c *RpcClient) Bash(cmd string) (BashResult, error) {
	return sendFor[BashResult](c, command("bash", field("command", cmd)))
}

// AbortBash aborts the running bash command.
func (c *RpcClient) AbortBash() error { return c.sendOnly(command("abort_bash")) }

// GetSessionStats returns session statistics.
func (c *RpcClient) GetSessionStats() (SessionStats, error) {
	return sendFor[SessionStats](c, command("get_session_stats"))
}

// ExportHtml exports the session to HTML.
func (c *RpcClient) ExportHtml(outputPath *string) (ExportHtmlResult, error) {
	return sendFor[ExportHtmlResult](c, command("export_html", optionalField("outputPath", outputPath)...))
}

// SwitchSession switches to another session file; the result reports whether
// an extension cancelled it.
func (c *RpcClient) SwitchSession(sessionPath string) (CancelledResult, error) {
	return sendFor[CancelledResult](c, command("switch_session", field("sessionPath", sessionPath)))
}

// Fork forks from a specific message.
func (c *RpcClient) Fork(entryID string) (ForkResult, error) {
	return sendFor[ForkResult](c, command("fork", field("entryId", entryID)))
}

// Clone clones the current active branch into a new session.
func (c *RpcClient) Clone() (CancelledResult, error) {
	return sendFor[CancelledResult](c, command("clone"))
}

// GetForkMessages lists the messages available for forking.
func (c *RpcClient) GetForkMessages() ([]ForkMessage, error) {
	data, err := sendFor[struct {
		Messages []ForkMessage `json:"messages"`
	}](c, command("get_fork_messages"))
	return data.Messages, err
}

// GetEntries returns session entries in append order, optionally only those
// after the since entry id.
func (c *RpcClient) GetEntries(since *string) (GetEntriesResult, error) {
	return sendFor[GetEntriesResult](c, command("get_entries", optionalField("since", since)...))
}

// GetTree returns the session entry tree.
func (c *RpcClient) GetTree() (GetTreeResult, error) {
	return sendFor[GetTreeResult](c, command("get_tree"))
}

// GetLastAssistantText returns the last assistant message text, or nil.
func (c *RpcClient) GetLastAssistantText() (*string, error) {
	data, err := sendFor[struct {
		Text *string `json:"text"`
	}](c, command("get_last_assistant_text"))
	return data.Text, err
}

// SetSessionName sets the session display name.
func (c *RpcClient) SetSessionName(name string) error {
	return c.sendOnly(command("set_session_name", field("name", name)))
}

// GetMessages returns all messages in the session.
func (c *RpcClient) GetMessages() ([]AgentMessage, error) {
	data, err := sendFor[struct {
		Messages []AgentMessage `json:"messages"`
	}](c, command("get_messages"))
	return data.Messages, err
}

// GetCommands lists extension commands, prompt templates, and skills.
func (c *RpcClient) GetCommands() ([]RpcSlashCommand, error) {
	data, err := sendFor[struct {
		Commands []RpcSlashCommand `json:"commands"`
	}](c, command("get_commands"))
	return data.Commands, err
}

// GetExtensions lists the resolved paths of the loaded extensions in load order.
func (c *RpcClient) GetExtensions() ([]string, error) {
	data, err := sendFor[struct {
		Paths []string `json:"paths"`
	}](c, command("get_extensions"))
	return data.Paths, err
}

// WaitForIdle waits for the next agent_settled event (upstream default
// timeout: DefaultTimeout).
func (c *RpcClient) WaitForIdle(timeout time.Duration) error {
	_, err := c.awaitSettled(timeout, false, "Timeout waiting for agent to become idle")
	return err
}

// CollectEvents collects events up to and including the next agent_settled
// event (upstream default timeout: DefaultTimeout).
func (c *RpcClient) CollectEvents(timeout time.Duration) ([]JsonAgentSessionEvent, error) {
	return c.awaitSettled(timeout, true, "Timeout collecting events")
}

// PromptAndWait sends a prompt and returns every event through agent_settled.
// Collection starts before the prompt is sent, as upstream's does.
func (c *RpcClient) PromptAndWait(message string, images []ImageContent, timeout time.Duration) ([]JsonAgentSessionEvent, error) {
	collected := c.startCollecting(true)
	if _, err := c.Prompt(message, images, nil); err != nil {
		collected.unsubscribe()
		return nil, err
	}
	return c.finishCollecting(collected, timeout, "Timeout collecting events")
}
