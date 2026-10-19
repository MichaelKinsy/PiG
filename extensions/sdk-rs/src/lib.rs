//! pig-sdk: Rust SDK for building pig subprocess extensions.
//!
//! Mirrors the Go SDK (`extensions/sdk/`): same wire protocol, same message
//! types, same lifecycle. Extensions connect to the host over a Unix socket,
//! register tools/commands/events, then process requests until shutdown.
//!
//! # Example
//! ```no_run
//! use pig_sdk::{empty_schema, Extension, ToolResult};
//!
//! fn main() {
//!     let mut ext = Extension::new("my-ext");
//!     ext.tool("hello", "Say hello", empty_schema(), |_ctx, _params| {
//!         ToolResult::text("Hello from Rust!")
//!     });
//!     ext.run().unwrap();
//! }
//! ```
//!
//! # Pi's helper exports
//!
//! Extension authors port Pi helpers with the same names in snake case. The truncation helpers run as Pi's truncate.ts does:
//!
//! ```
//! use pig_sdk::{format_size, truncate_head, truncate_tail, TruncatedBy, TruncationOptions};
//!
//! // Pi's truncateTail keeps the end of command output; an omitted limit takes Pi's default (2000 lines, 50KB).
//! let output = truncate_tail("line 1\nline 2\nline 3\n", TruncationOptions { max_lines: Some(2), ..Default::default() });
//! assert_eq!((output.content.as_str(), output.truncated_by), ("line 2\nline 3", Some(TruncatedBy::Lines)));
//! assert_eq!(format!("showing {} of {}", format_size(output.output_bytes), format_size(output.total_bytes)), "showing 13B of 21B");
//!
//! // Pi's truncateHead never returns a partial line.
//! let head = truncate_head("a\nb\nc\n", TruncationOptions { max_lines: Some(2), ..Default::default() });
//! assert_eq!(head.content, "a\nb");
//! assert_eq!(format_size(1280), "1.3KB");
//! ```
//!
//! The file mutation queue and the host configuration need a PiG host, so this example is compile-checked only:
//!
//! ```no_run
//! use pig_sdk::{get_agent_dir, with_file_mutation_queue};
//!
//! fn main() -> std::io::Result<()> {
//!     // Pi's withFileMutationQueue: parallel tool calls on one file run one at a time, in call order.
//!     let notes = std::path::Path::new(&get_agent_dir()?).join("notes.txt");
//!     with_file_mutation_queue(&notes, || std::fs::write(&notes, "saved"))??;
//!     Ok(())
//! }
//! ```
//!
//! `get_agent_dir`, `get_config_dir_name` and `get_version` are Pi's `getAgentDir()`, `CONFIG_DIR_NAME` and `VERSION`. They fail, instead of returning
//! a made-up path, when the process was not started by a PiG host.

mod event_stream;
mod autocomplete;
pub use autocomplete::{AutocompleteProvider, AutocompleteProviderFactory, AutocompleteSuggestions, AutocompleteCompletion, AutocompleteSuggestionsFn, AutocompleteApplyFn, AutocompleteFileTriggerFn};
mod provider;
pub use provider::{Provider, ProviderAuth, APIKeyAuth, OAuthAuth, APIKeyAuthInput, AuthContext, AuthResult, AuthCheck, AuthInteraction, LoginOptions, ProviderStreamOptions, ProviderStreamFn, ProviderOperationFn, ProviderOperationOptions, ProviderOperations, ProviderStreamSimpleFn, ProviderFilterFn, ProviderModel, ProviderResult, ProviderSignal, ProviderSignalSubscription, ModelsPublication, RefreshModelsContext};
mod constrained_sampling;
pub use constrained_sampling::ToolConstrainedSampling;
mod context;
#[cfg(test)]
mod model_info_tests;
#[cfg(test)]
mod branch_entry_tests;
#[cfg(test)]
mod session_mirror_append_tests;
mod editor_component;
mod event_bus;
#[cfg(all(test, unix))]
mod event_bus_tests;
pub use event_bus::{EventBus, EventBusHandler, Subscription};
mod events;
mod extension_api;
mod footer_data;
pub use footer_data::{BranchChangeSubscription, FooterData};
pub use extension_api::{
    AgentTool, AgentToolCall, AgentToolCallOutcome, AgentToolResult, ExecuteToolOptions,
    ExecuteToolUpdate, ModelRoute, ModelRouteFn, ModelRouteRequest, RegisteredMcpServer,
    ToolAnnotations, ToolExposure, ToolLoadout, ToolLoadoutChanges, ToolNamespace,
    ToolPrepareLoadout, ToolReach, ToolReachesTools, VirtualModel,
};
mod replaced_session;
mod setup_session;
pub use setup_session::{SetupFn, SetupSessionManager};
pub use replaced_session::{ReplacedSessionContext, WithSessionFn};
mod session_manager;
pub use session_manager::SessionManager;
mod host_info;
pub use host_info::{get_agent_dir, get_config_dir_name, get_version};
mod file_mutation_queue;
pub use file_mutation_queue::with_file_mutation_queue;
mod language;
pub use language::get_language_from_path;
mod compaction_cutpoint;
pub use compaction_cutpoint::{CutPointResult, find_cut_point, find_turn_start_index};
mod compaction_estimate;
pub use compaction_estimate::estimate_tokens;
mod image_dimension_note;
pub use image_dimension_note::{ResizedImage, format_dimension_note};
mod llm_convert;
pub use llm_convert::convert_to_llm;
mod serialize_conversation;
pub use serialize_conversation::{serialize_conversation, serialize_conversation_json};
mod branch_summary;
mod lossless_json;
pub use branch_summary::{BranchPreparation, CollectEntriesResult, FileOperations, SessionReader, collect_entries_for_branch_summary, prepare_branch_entries, prepare_branch_entries_json};
mod host_helpers;
pub use host_helpers::{ParsedFrontmatter, parse_date, parse_frontmatter, strip_frontmatter};
mod compaction_usage;
pub use compaction_usage::{CompactionSettings, DEFAULT_COMPACTION_SETTINGS, calculate_context_tokens, get_last_assistant_usage, should_compact};
mod session_skill_helpers;
pub use session_skill_helpers::{CURRENT_SESSION_VERSION, ParsedSkillBlock, VIRTUAL_MODEL_STATE_ENTRY, format_skills_for_prompt, get_latest_compaction_entry, parse_skill_block};
mod truncate;
pub use truncate::{DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, GREP_MAX_LINE_LENGTH, TruncatedBy, TruncationOptions, TruncationResult, format_size, truncate_head, truncate_line, truncate_tail};
mod extension;
pub mod kit;
mod login;
mod oauth;
mod protocol;
mod theme;
mod theme_color;
mod tool_render;
mod tool_start_order;
mod transport;
mod user_bash;
pub use user_bash::{BashExecFn, BashExecOptions, BashOperations};

mod js_string;
pub use js_string::JsString;
pub use editor_component::{EditorBase, EditorComponent, EditorCursor, EditorFactory, EditorMouseEvent};

pub use context::{
    CommandInfo, CompactCompleteHandler, CompactErrorHandler, CompactOptions, Context,
    CustomMessage, DialogOptions, ExecOptions, ExecResult, ModelEventStream, ModelFetchRequest, ModelFetchResponse, ModelRegistry, ModelStreamCallbacks, MouseEvent, OverlayBounds, OverlayHandle, OverlayState, UnfocusTarget,
    RemoteComponent, RemoteComponentInvalidate, RemoteComponentResult, SendMessageOptions,
    SourceInfo, UsageInfo, TerminalInputResult, TerminalInputSubscription, ToolInfo, ToolInfoReachesTools, ViewComponent, message_role,
    message_text, is_tool_call_event_type, is_bash_tool_result, is_powershell_tool_result, is_read_tool_result, is_edit_tool_result, is_write_tool_result, is_grep_tool_result, is_find_tool_result, is_ls_tool_result,
};
pub use events::*;
#[doc(hidden)]
pub use extension::report_load_failure;
pub use extension::{
    context_unchanged, EventSubscriber, EventSubscription,
    CommandResult, Extension, Factory, FlagOptions, FlagType, MarkdownTransformContext, ProjectTrustDecision, ProjectTrustResult, ToolDefinition,
    ToolHandler, ToolPrepareArguments, ToolResult,
};
pub use login::{LoginDefinition, SpriteDefinition};
pub use oauth::{
    OAUTH_CANCELLED, OAuthAuthInfo, OAuthCredentialStatus, OAuthCredentialStore, OAuthCredentials,
    OAuthDeviceCodeInfo, OAuthGetApiKeyFn, OAuthLoginCallbacks, OAuthLoginFn, OAuthPrompt,
    OAuthProvider, OAuthRefreshFn, OAuthSelectOption, OAuthSelectPrompt,
};
pub use protocol::{AutocompleteItem, ConstrainedSampling, Schema, empty_schema};
pub use theme::{Color, TextAttributes, Theme, ThemeAppearance, ThemeColorFn, ThemeSlot, ThemeStyle};
pub use tool_render::{
    SharedToolRenderCall, SharedToolRenderCallView, SharedToolRenderResult, SharedToolRenderResultView,
    ToolRenderCallHandler, ToolRenderCallViewHandler, ToolRenderContext, ToolRenderResult, ToolRenderResultHandler,
    ToolRenderResultOptions, ToolRenderResultViewHandler, ToolRenderShell, ToolRendererResolver, ToolRendererSet,
};
