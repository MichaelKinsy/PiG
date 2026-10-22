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
mod pi_ai_helpers_tests;
#[cfg(test)]
mod model_info_tests;
#[cfg(test)]
mod theme_helper_tests;
#[cfg(test)]
mod branch_entry_tests;
#[cfg(test)]
mod session_mirror_append_tests;
mod editor_component;
mod event_bus;
#[cfg(all(test, unix))]
mod event_bus_tests;
pub use event_bus::{EventBus, EventBusHandler, Subscription};
mod before_agent_start;
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
pub use session_manager::ReadonlySessionManager;
mod graph;
mod session_manager_class;
pub use session_manager_class::{ListProgress, NewSessionOptions, SessionInfo, SessionManager};
mod settings_manager;
pub use settings_manager::{SettingsError, SettingsManager, SettingsManagerCreateOptions, SettingsStorage};
mod model_runtime;
pub use model_runtime::{ModelRuntime, ModelRuntimeCreateOptions};
mod package_manager;
pub use package_manager::{DefaultPackageManager, PackageManagerOptions};
mod resource_loader;
pub use resource_loader::{DefaultResourceLoader, DefaultResourceLoaderOptions, InlineExtensionFactory, LoaderOverride};
mod project_trust;
pub use project_trust::{has_trust_requiring_project_resources, ProjectTrustStore, ProjectTrustStoreEntry, ProjectTrustUpdate};
mod host_info;
pub use host_info::{ShellConfig, get_agent_dir, get_config_dir_name, get_package_dir, get_shell_config, get_version};
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
mod pi_ai_helpers;
mod session_entry_messages;
pub use session_entry_messages::session_entry_to_context_messages;
mod stored_credential;
pub use stored_credential::read_stored_credential;
pub use pi_ai_helpers::{
    DEFAULT_MAX_AGENT_RETRY_DELAY_MS, RetryPolicy, StringEnumOptions, TranscriptToolSupport, TranscriptTools, calculate_cost, clamp_thinking_level, collapse_system_messages, content_text, create_initial_system_message,
    env_api_key_auth, get_model_type, has_api, is_model_type, models_are_equal, get_current_system_message, get_current_system_prompt, get_current_tools, get_declared_tools, get_initial_system_message, get_supported_thinking_levels,
    get_system_message_text, has_non_additive_tool_changes, is_context_overflow, is_recoverable_length, is_retryable_assistant_error, normalize_context, render_system_message_update,
    resolve_transcript, resolve_transcript_tools, retry_delay_ms, string_enum, without_initial_system_message,
};
mod serialize_conversation;
pub use serialize_conversation::{serialize_conversation, serialize_conversation_json};
mod branch_summary;
mod lossless_json;
pub use branch_summary::{BranchPreparation, CollectEntriesResult, FileOperations, SessionReader, collect_entries_for_branch_summary, prepare_branch_entries, prepare_branch_entries_json};
mod host_helpers;
pub use host_helpers::{ConvertedImage, DiffString, ImageResizeOptions, ParsedFrontmatter, convert_to_png, copy_to_clipboard, generate_diff_string, generate_unified_patch, parse_date, parse_frontmatter, resize_image, strip_frontmatter};
mod compaction_usage;
pub use compaction_usage::{CompactionSettings, DEFAULT_COMPACTION_SETTINGS, calculate_context_tokens, get_last_assistant_usage, should_compact};
mod session_skill_helpers;
pub use session_skill_helpers::{CURRENT_SESSION_VERSION, ParsedSkillBlock, VIRTUAL_MODEL_STATE_ENTRY, format_skills_for_prompt, get_latest_compaction_entry, parse_skill_block};
mod truncate;
pub use truncate::{DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, GREP_MAX_LINE_LENGTH, TruncatedBy, TruncationOptions, TruncationResult, format_size, truncate_head, truncate_line, truncate_tail};
mod extension;
pub mod kit;
pub mod tui;
mod login;
mod oauth;
mod protocol;
mod host_subscriptions;
mod theme;
mod visual_truncate;
pub use visual_truncate::{VisualKeep, VisualTruncateResult, truncate_to_visual_lines};
mod keybinding_hints;
pub use keybinding_hints::{KeyTextFormatOptions, format_key_text, key_display_text, key_hint, key_text, raw_key_hint};
mod theme_helpers;
pub use theme_helpers::{EditorTheme, HighlightFn, MarkdownTheme, SelectListTheme, SelectedStyleFn, SettingsListTheme, StyleFn, get_editor_theme, get_markdown_theme, get_select_list_theme, get_settings_list_theme, highlight_code, init_theme};
mod tool_render;
mod request_start_order;
mod tool_start_order;
mod transport;
mod user_bash;
pub use user_bash::{BashExecFn, BashExecOptions, BashOperations};
mod builtin_tool;
pub use builtin_tool::{
    BashSpawnContext, BashSpawnHook, BashToolOptions, BuiltinTool, BuiltinToolDefinition, BuiltinToolExecutor, EditOperations, EditToolOptions, FindGlobOptions, FindOperations,
    FindToolOptions, GrepOperations, GrepToolOptions, LocalBashOperationsOptions, LsOperations, LsStat, LsToolOptions, ModelImageResizeOptions, OperationResult, PathOperation,
    PowerShellToolOptions, ReadOperations, ReadToolOptions, ToolsOptions, WriteOperation, WriteOperations, WriteToolOptions, create_bash_tool, create_bash_tool_definition,
    create_coding_tools, create_edit_tool, create_edit_tool_definition, create_find_tool, create_find_tool_definition, create_grep_tool, create_grep_tool_definition,
    create_local_bash_operations, create_local_powershell_operations, create_ls_tool, create_ls_tool_definition, create_powershell_tool, create_powershell_tool_definition,
    create_read_only_tools, create_read_tool, create_read_tool_definition, create_write_tool, create_write_tool_definition,
};

mod js_string;
pub use js_string::JsString;
pub use editor_component::{
    EditorActionHandlers, EditorBase, EditorComponent, EditorCursor, EditorFactory, EditorHook, EditorMouseEvent, EditorShortcutHook, EditorStatus,
};

pub use context::{
    CommandInfo, CompactCompleteHandler, CompactErrorHandler, CompactOptions, Context,
    CustomMessage, DialogOptions, ExecOptions, ExecResult, ModelEventStream, create_assistant_message_event_stream, ModelFetchRequest, ModelFetchResponse, ModelRegistry, ModelStreamCallbacks, MouseEvent, OverlayBounds, OverlayHandle, OverlayState, UnfocusTarget,
    RemoteComponent, RemoteComponentInvalidate, RemoteComponentResult, SendMessageOptions,
    SourceInfo, UsageInfo, TerminalInputResult, TerminalInputSubscription, ToolInfo, ToolInfoReachesTools, ViewComponent, message_role,
    message_text, is_tool_call_event_type, is_bash_tool_result, is_powershell_tool_result, is_read_tool_result, is_edit_tool_result, is_write_tool_result, is_grep_tool_result, is_find_tool_result, is_ls_tool_result,
};
pub use before_agent_start::BeforeAgentStartEvent;
pub use events::*;
mod child_session;
pub use child_session::{AgentSession, CreateAgentSessionOptions, PromptOptions, ResourceOptions, SessionSubscription};
pub use host_subscriptions::HostSubscription;
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
