//! The members of Pi's `AgentSession` that an extension reaches over the wire, after the ones in `child_session.rs`.
//!
//! Each is the host's own `AgentSession` member (`childSession.op`): the value is read from, or the change made on, the session the host owns, so a
//! call returns what the session holds now. A member Pi types `X | undefined` returns `None` (or `Null` for a raw value) for undefined.
//!
//! Async contract. A member Pi declares `async` blocks until its Promise settles and returns its value or error ([`AgentSession::compact`],
//! [`AgentSession::navigate_tree`], [`AgentSession::reload`], [`AgentSession::wait_for_idle`], [`AgentSession::execute_bash`],
//! [`AgentSession::export_to_html`], [`AgentSession::summarize_for_bug_report`], [`AgentSession::set_model`],
//! [`AgentSession::send_custom_message`], [`AgentSession::send_user_message`]). It starts in call order, and a later call made from another
//! thread, an abort among them ([`AgentSession::abort_compaction`], [`AgentSession::abort_branch_summary`], [`AgentSession::abort_bash`]), never
//! waits for it. A member Pi declares synchronous is a blocking call that returns the host's error with its value.
//!
//! Scope: a tool's `execute` and hooks are functions of the extension that registered it and do not cross the wire
//! ([`AgentSession::get_all_tools`] returns the definitions' data, `reaches_tools` is `None`), and `execute_bash` carries no `onChunk`: the output
//! chunks reach [`AgentSession::subscribe`] listeners as the session's events do.

use std::io;

use serde::de::DeserializeOwned;
use serde_json::{Value, json};

use crate::child_session::AgentSession;
use crate::context::ContextUsage;
use crate::{CustomMessage, NavigateTreeOptions, SendMessageOptions, ToolInfo};

/// Pi's `ModelMutationOptions`: `persist` writes the choice to the global defaults.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct ModelMutationOptions {
    pub persist: bool,
}

/// The options of [`AgentSession::execute_bash`] and [`AgentSession::record_bash_result`].
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct ExecuteBashOptions {
    /// Keeps the result out of the LLM context (the `!!cmd` semantics); `None` leaves it unset.
    pub exclude_from_context: Option<bool>,
}

/// The options of [`AgentSession::send_user_message`].
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SendUserMessageOptions {
    /// `"steer"` or `"followUp"`.
    pub deliver_as: Option<String>,
    pub expand_prompt_templates: Option<bool>,
}

/// One entry of [`AgentSession::get_user_messages_for_forking`].
#[derive(Debug, Clone, PartialEq, Eq, serde::Deserialize)]
pub struct ForkMessage {
    #[serde(rename = "entryId")]
    pub entry_id: String,
    pub text: String,
}

/// The value of [`AgentSession::clear_queue`]: the messages it removed.
#[derive(Debug, Clone, Default, PartialEq, Eq, serde::Deserialize)]
pub struct QueuedMessages {
    #[serde(default)]
    pub steering: Vec<String>,
    #[serde(default, rename = "followUp")]
    pub follow_up: Vec<String>,
}

fn invalid(op: &str, error: serde_json::Error) -> io::Error {
    io::Error::new(
        io::ErrorKind::InvalidData,
        format!("host reply to child session member {op}: {error}"),
    )
}

impl AgentSession {
    fn op(&self, method: &str, op: &str, args: Option<Value>) -> io::Result<Value> {
        let mut payload = json!({"op": op});
        if let Some(args) = args {
            payload["args"] = args;
        }
        Ok(self.call(method, payload)?.unwrap_or(Value::Null))
    }

    /// A synchronous member and its value (`Null` for undefined or none).
    fn member(&self, op: &str, args: Option<Value>) -> io::Result<Value> {
        self.op("childSession.op", op, args)
    }

    /// A member Pi awaits, as Pi's Promise: it returns when it settled.
    fn member_async(&self, op: &str, args: Option<Value>) -> io::Result<Value> {
        self.op("childSession.opAsync", op, args)
    }

    fn decode<T: DeserializeOwned>(&self, op: &str, args: Option<Value>) -> io::Result<T> {
        serde_json::from_value(self.member(op, args)?).map_err(|error| invalid(op, error))
    }

    /// A member whose value is a list Pi never leaves undefined.
    fn decode_list<T: DeserializeOwned>(&self, op: &str) -> io::Result<Vec<T>> {
        match self.member(op, None)? {
            Value::Null => Ok(Vec::new()),
            value => serde_json::from_value(value).map_err(|error| invalid(op, error)),
        }
    }

    // ── State reads ──────────────────────────────────────────────────────────

    /// `session.isStreaming`.
    pub fn is_streaming(&self) -> io::Result<bool> {
        self.decode("isStreaming", None)
    }

    /// `session.isIdle`: neither streaming nor compacting.
    pub fn is_idle(&self) -> io::Result<bool> {
        self.decode("isIdle", None)
    }

    /// `session.isCompacting`.
    pub fn is_compacting(&self) -> io::Result<bool> {
        self.decode("isCompacting", None)
    }

    /// `session.isRetrying`.
    pub fn is_retrying(&self) -> io::Result<bool> {
        self.decode("isRetrying", None)
    }

    /// `session.isBashRunning`.
    pub fn is_bash_running(&self) -> io::Result<bool> {
        self.decode("isBashRunning", None)
    }

    /// `session.hasPendingBashMessages`: bash results wait to be flushed after the agent turn.
    pub fn has_pending_bash_messages(&self) -> io::Result<bool> {
        self.decode("hasPendingBashMessages", None)
    }

    /// `session.retryAttempt`.
    pub fn retry_attempt(&self) -> io::Result<u64> {
        self.decode("retryAttempt", None)
    }

    /// `session.pendingMessageCount`: the queued steering and follow-up messages.
    pub fn pending_message_count(&self) -> io::Result<u64> {
        self.decode("pendingMessageCount", None)
    }

    /// `session.sessionId`.
    pub fn session_id(&self) -> io::Result<String> {
        self.decode("sessionId", None)
    }

    /// `session.sessionFile`; `None` for an in-memory session, Pi's undefined.
    pub fn session_file(&self) -> io::Result<Option<String>> {
        self.decode("sessionFile", None)
    }

    /// `session.sessionName`; `None` when the session has no name, Pi's undefined.
    pub fn session_name(&self) -> io::Result<Option<String>> {
        self.decode("sessionName", None)
    }

    /// `session.systemPrompt`.
    pub fn system_prompt(&self) -> io::Result<String> {
        self.decode("systemPrompt", None)
    }

    /// `session.thinkingLevel`.
    pub fn thinking_level(&self) -> io::Result<String> {
        self.decode("thinkingLevel", None)
    }

    /// `session.model`: the session's Pi `Model` object, `None` without one.
    pub fn model(&self) -> io::Result<Option<Value>> {
        self.decode("model", None)
    }

    /// `session.routedModel`: under a virtual selection, `{model, thinkingLevel?}` of the latest successful response, `None` otherwise.
    pub fn routed_model(&self) -> io::Result<Option<Value>> {
        self.decode("routedModel", None)
    }

    /// `session.scopedModels`: the `{model, thinkingLevel?}` entries the session cycles through, empty for all available models.
    pub fn scoped_models(&self) -> io::Result<Vec<Value>> {
        self.decode_list("scopedModels")
    }

    /// `session.steeringMode`: `"all"` or `"one-at-a-time"`.
    pub fn steering_mode(&self) -> io::Result<String> {
        self.decode("steeringMode", None)
    }

    /// `session.followUpMode`: `"all"` or `"one-at-a-time"`.
    pub fn follow_up_mode(&self) -> io::Result<String> {
        self.decode("followUpMode", None)
    }

    /// `session.autoCompactionEnabled`.
    pub fn auto_compaction_enabled(&self) -> io::Result<bool> {
        self.decode("autoCompactionEnabled", None)
    }

    /// `session.autoRetryEnabled`.
    pub fn auto_retry_enabled(&self) -> io::Result<bool> {
        self.decode("autoRetryEnabled", None)
    }

    /// `session.supportsThinking()`.
    pub fn supports_thinking(&self) -> io::Result<bool> {
        self.decode("supportsThinking", None)
    }

    /// `session.getAvailableThinkingLevels()`.
    pub fn get_available_thinking_levels(&self) -> io::Result<Vec<String>> {
        self.decode_list("getAvailableThinkingLevels")
    }

    /// `session.getLastAssistantText()`; `None` when the session has none, Pi's undefined.
    pub fn get_last_assistant_text(&self) -> io::Result<Option<String>> {
        self.decode("getLastAssistantText", None)
    }

    /// `session.getActiveToolNames()`.
    pub fn get_active_tool_names(&self) -> io::Result<Vec<String>> {
        self.decode_list("getActiveToolNames")
    }

    /// `session.getCallableToolNames()`.
    pub fn get_callable_tool_names(&self) -> io::Result<Vec<String>> {
        self.decode_list("getCallableToolNames")
    }

    /// `session.getSteeringMessages()`: the steering messages that have not been delivered.
    pub fn get_steering_messages(&self) -> io::Result<Vec<String>> {
        self.decode_list("getSteeringMessages")
    }

    /// `session.getFollowUpMessages()`: the follow-up messages that have not been delivered.
    pub fn get_follow_up_messages(&self) -> io::Result<Vec<String>> {
        self.decode_list("getFollowUpMessages")
    }

    /// `session.getAllTools()`: the definitions' data; `reaches_tools` is `None`, a function of the extension that registered the tool.
    pub fn get_all_tools(&self) -> io::Result<Vec<ToolInfo>> {
        self.decode_list("getAllTools")
    }

    /// `session.getToolDefinition(name)`: the definition's data, `None` when no tool has the name. Its `execute` and hooks run in the extension that registered it and are not carried.
    pub fn get_tool_definition(&self, name: &str) -> io::Result<Option<ToolInfo>> {
        self.decode("getToolDefinition", Some(json!({"name": name})))
    }

    /// `session.getContextUsage()`: `None` when no usable model context window is available.
    pub fn get_context_usage(&self) -> io::Result<Option<ContextUsage>> {
        let value = self.member("getContextUsage", None)?;
        if value.is_null() {
            return Ok(None);
        }
        let window = value["contextWindow"].as_i64().ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                "host reply to child session member getContextUsage has no contextWindow",
            )
        })?;
        Ok(Some(ContextUsage {
            tokens: value["tokens"].as_i64().map(|tokens| tokens as i32),
            context_window: window as i32,
            percent: value["percent"].as_f64(),
        }))
    }

    /// `session.getSessionStats()`: Pi's `SessionStats` (`sessionFile?`, `sessionId`, `userMessages`, `assistantMessages`, `toolCalls`, `toolResults`, `totalMessages`, `tokens`, `cost`, `contextUsage?`).
    pub fn get_session_stats(&self) -> io::Result<Value> {
        self.member("getSessionStats", None)
    }

    /// `session.getUserMessagesForForking()`.
    pub fn get_user_messages_for_forking(&self) -> io::Result<Vec<ForkMessage>> {
        self.decode_list("getUserMessagesForForking")
    }

    /// `session.promptTemplates`: Pi's `PromptTemplate` objects.
    pub fn prompt_templates(&self) -> io::Result<Vec<Value>> {
        self.decode_list("promptTemplates")
    }

    /// `session.cacheWarmingStatus`: Pi's `CacheWarmingStatus`, `None` when the session has no cache warmer.
    pub fn cache_warming_status(&self) -> io::Result<Option<Value>> {
        self.decode("cacheWarmingStatus", None)
    }

    /// `session.hasExtensionHandlers(eventType)`: an extension the session loaded handles the event.
    pub fn has_extension_handlers(&self, event_type: &str) -> io::Result<bool> {
        self.decode(
            "hasExtensionHandlers",
            Some(json!({"eventType": event_type})),
        )
    }

    // ── Changes ──────────────────────────────────────────────────────────────

    /// `session.setModel(model, options)`: `model` is a Pi `Model` object whose provider and id name a model of the host's registry. It fails for a model whose provider has no credentials.
    pub fn set_model(&self, model: &Value, options: ModelMutationOptions) -> io::Result<()> {
        self.member_async(
            "setModel",
            Some(json!({"model": model, "persist": options.persist})),
        )
        .map(|_| ())
    }

    /// `session.cycleModel(direction, options)`: `direction` is `"forward"` (or empty) or `"backward"`. The `{model, thinkingLevel, isScoped}` result is `None` when there is no model to cycle to.
    pub fn cycle_model(
        &self,
        direction: &str,
        options: ModelMutationOptions,
    ) -> io::Result<Option<Value>> {
        let mut args = json!({"persist": options.persist});
        if !direction.is_empty() {
            args["direction"] = json!(direction);
        }
        serde_json::from_value(self.member_async("cycleModel", Some(args))?)
            .map_err(|error| invalid("cycleModel", error))
    }

    /// `session.setThinkingLevel(level, options)`; the level is clamped to the model.
    pub fn set_thinking_level(&self, level: &str, options: ModelMutationOptions) -> io::Result<()> {
        self.member(
            "setThinkingLevel",
            Some(json!({"level": level, "persist": options.persist})),
        )
        .map(|_| ())
    }

    /// `session.cycleThinkingLevel(options)`: the new level, `None` when the model does not support thinking, Pi's undefined.
    pub fn cycle_thinking_level(
        &self,
        options: ModelMutationOptions,
    ) -> io::Result<Option<String>> {
        self.decode(
            "cycleThinkingLevel",
            Some(json!({"persist": options.persist})),
        )
    }

    /// `session.setScopedModels(models)`: `{model, thinkingLevel?}` entries.
    pub fn set_scoped_models(&self, models: &[Value]) -> io::Result<()> {
        self.member("setScopedModels", Some(json!({"models": models})))
            .map(|_| ())
    }

    /// `session.setActiveToolsByName(names)`.
    pub fn set_active_tools_by_name<S: AsRef<str>>(&self, names: &[S]) -> io::Result<()> {
        let names: Vec<&str> = names.iter().map(AsRef::as_ref).collect();
        self.member("setActiveToolsByName", Some(json!({"names": names})))
            .map(|_| ())
    }

    /// `session.setSteeringMode(mode)`: `"all"` or `"one-at-a-time"`.
    pub fn set_steering_mode(&self, mode: &str) -> io::Result<()> {
        self.member("setSteeringMode", Some(json!({"mode": mode})))
            .map(|_| ())
    }

    /// `session.setFollowUpMode(mode)`: `"all"` or `"one-at-a-time"`.
    pub fn set_follow_up_mode(&self, mode: &str) -> io::Result<()> {
        self.member("setFollowUpMode", Some(json!({"mode": mode})))
            .map(|_| ())
    }

    /// `session.setAutoCompactionEnabled(enabled)`.
    pub fn set_auto_compaction_enabled(&self, enabled: bool) -> io::Result<()> {
        self.member(
            "setAutoCompactionEnabled",
            Some(json!({"enabled": enabled})),
        )
        .map(|_| ())
    }

    /// `session.setAutoRetryEnabled(enabled)`.
    pub fn set_auto_retry_enabled(&self, enabled: bool) -> io::Result<()> {
        self.member("setAutoRetryEnabled", Some(json!({"enabled": enabled})))
            .map(|_| ())
    }

    /// `session.setCacheWarmingMode(mode)`: `"off"`, `"streaming"` or `"idle"`.
    pub fn set_cache_warming_mode(&self, mode: &str) -> io::Result<()> {
        self.member("setCacheWarmingMode", Some(json!({"mode": mode})))
            .map(|_| ())
    }

    /// `session.setSessionName(name)`.
    pub fn set_session_name(&self, name: &str) -> io::Result<()> {
        self.member("setSessionName", Some(json!({"name": name})))
            .map(|_| ())
    }

    /// `session.clearQueue()`: removes the queued steering and follow-up messages and returns them.
    pub fn clear_queue(&self) -> io::Result<QueuedMessages> {
        self.decode("clearQueue", None)
    }

    /// `session.abortBash()`.
    pub fn abort_bash(&self) -> io::Result<()> {
        self.member("abortBash", None).map(|_| ())
    }

    /// `session.abortBranchSummary()`.
    pub fn abort_branch_summary(&self) -> io::Result<()> {
        self.member("abortBranchSummary", None).map(|_| ())
    }

    /// `session.abortCompaction()`.
    pub fn abort_compaction(&self) -> io::Result<()> {
        self.member("abortCompaction", None).map(|_| ())
    }

    /// `session.abortRetry()`.
    pub fn abort_retry(&self) -> io::Result<()> {
        self.member("abortRetry", None).map(|_| ())
    }

    // ── Awaited members ──────────────────────────────────────────────────────

    /// `session.waitForIdle()`: returns when the session is neither streaming nor compacting.
    pub fn wait_for_idle(&self) -> io::Result<()> {
        self.member_async("waitForIdle", None).map(|_| ())
    }

    /// `session.reload()`: settings, extensions and resources are loaded again. Pi rejects it while the session runs or compacts.
    pub fn reload(&self) -> io::Result<()> {
        self.member_async("reload", None).map(|_| ())
    }

    /// `session.compact(customInstructions)`: manual compaction, Pi's `CompactionResult`. A session with nothing to compact is an error, as Pi's rejection. [`AgentSession::abort_compaction`] from another thread cancels it.
    pub fn compact(&self, custom_instructions: Option<&str>) -> io::Result<Value> {
        let args = custom_instructions.map(|text| json!({"customInstructions": text}));
        self.member_async("compact", Some(args.unwrap_or_else(|| json!({}))))
    }

    /// `session.navigateTree(targetId, options)`: `options` is Pi's options object (`summarize?`, `customInstructions?`, `replaceInstructions?`, `label?`), `Null` for none. The result is `{cancelled, aborted?, editorText?, summaryEntry?}`. [`AgentSession::abort_branch_summary`] from another thread cancels the summary.
    pub fn navigate_tree(
        &self,
        target_id: &str,
        options: NavigateTreeOptions,
    ) -> io::Result<Value> {
        let mut args = json!({"targetId": target_id});
        if let Value::Object(options) = options {
            for (key, value) in options {
                args[key] = value;
            }
        }
        self.member_async("navigateTree", Some(args))
    }

    /// `session.executeBash(command, onChunk, options)`: the command runs outside the agent loop and the result, `{output, exitCode?, cancelled, truncated, fullOutputPath?}`, is recorded for the next turn. [`AgentSession::abort_bash`] from another thread cancels it.
    pub fn execute_bash(&self, command: &str, options: ExecuteBashOptions) -> io::Result<Value> {
        let mut args = json!({"command": command});
        if let Some(exclude) = options.exclude_from_context {
            args["excludeFromContext"] = json!(exclude);
        }
        self.member_async("executeBash", Some(args))
    }

    /// `session.recordBashResult(command, result, options)`: `result` is Pi's `BashResult`.
    pub fn record_bash_result(
        &self,
        command: &str,
        result: &Value,
        options: ExecuteBashOptions,
    ) -> io::Result<()> {
        let mut args = json!({"command": command, "result": result});
        if let Some(exclude) = options.exclude_from_context {
            args["excludeFromContext"] = json!(exclude);
        }
        self.member("recordBashResult", Some(args)).map(|_| ())
    }

    /// `session.exportToHtml(outputPath, options)`: writes the session as an HTML file and returns its path. `None` is Pi's default location; `theme_name` exports with that theme when it is registered.
    pub fn export_to_html(
        &self,
        output_path: Option<&str>,
        theme_name: Option<&str>,
    ) -> io::Result<String> {
        let mut args = json!({});
        if let Some(path) = output_path {
            args["outputPath"] = json!(path);
        }
        if let Some(theme) = theme_name {
            args["themeName"] = json!(theme);
        }
        serde_json::from_value(self.member_async("exportToHtml", Some(args))?)
            .map_err(|error| invalid("exportToHtml", error))
    }

    /// `session.exportToJsonl(outputPath)`: writes the session as JSONL and returns its path. `None` is Pi's default location.
    pub fn export_to_jsonl(&self, output_path: Option<&str>) -> io::Result<String> {
        let args = output_path.map_or_else(|| json!({}), |path| json!({"outputPath": path}));
        self.decode("exportToJsonl", Some(args))
    }

    /// `session.summarizeForBugReport({hint})`.
    pub fn summarize_for_bug_report(&self, hint: Option<&str>) -> io::Result<String> {
        let args = hint.map_or_else(|| json!({}), |hint| json!({"hint": hint}));
        serde_json::from_value(self.member_async("summarizeForBugReport", Some(args))?)
            .map_err(|error| invalid("summarizeForBugReport", error))
    }

    /// `session.sendCustomMessage(message, options)`.
    pub fn send_custom_message(
        &self,
        message: &CustomMessage,
        options: &SendMessageOptions,
    ) -> io::Result<()> {
        let mut wire = json!({"customType": message.custom_type, "content": message.content, "display": message.display});
        if let Some(details) = &message.details {
            wire["details"] = details.clone();
        }
        let mut opts = json!({});
        if let Some(trigger) = options.trigger_turn {
            opts["triggerTurn"] = json!(trigger);
        }
        if let Some(deliver) = &options.deliver_as {
            opts["deliverAs"] = json!(deliver);
        }
        self.member_async(
            "sendCustomMessage",
            Some(json!({"message": wire, "options": opts})),
        )
        .map(|_| ())
    }

    /// `session.sendUserMessage(content, options)`: `content` is a string or an array of text and image content blocks.
    pub fn send_user_message(
        &self,
        content: Value,
        options: &SendUserMessageOptions,
    ) -> io::Result<()> {
        let mut opts = json!({});
        if let Some(deliver) = &options.deliver_as {
            opts["deliverAs"] = json!(deliver);
        }
        if let Some(expand) = options.expand_prompt_templates {
            opts["expandPromptTemplates"] = json!(expand);
        }
        self.member_async(
            "sendUserMessage",
            Some(json!({"content": content, "options": opts})),
        )
        .map(|_| ())
    }
}
