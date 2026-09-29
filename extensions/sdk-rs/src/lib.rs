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

mod autocomplete;
pub use autocomplete::{AutocompleteProvider, AutocompleteProviderFactory, AutocompleteSuggestions, AutocompleteCompletion, AutocompleteSuggestionsFn, AutocompleteApplyFn, AutocompleteFileTriggerFn};
mod provider;
pub use provider::{Provider, ProviderAuth, APIKeyAuth, OAuthAuth, APIKeyAuthInput, AuthContext, AuthResult, AuthCheck, AuthInteraction, ProviderStreamOptions, ProviderStreamFn, ProviderFilterFn, ProviderModel, ProviderResult, ProviderSignal, ProviderSignalSubscription, ModelsPublication, RefreshModelsContext};
mod constrained_sampling;
pub use constrained_sampling::ToolConstrainedSampling;
mod context;
mod events;
mod session_manager;
pub use session_manager::SessionManager;
mod extension;
mod login;
mod oauth;
mod protocol;
mod theme;
mod tool_render;
mod transport;
mod user_bash;

mod js_string;
pub use js_string::JsString;

pub use context::{
    CommandInfo, CompactCompleteHandler, CompactErrorHandler, CompactOptions, Context,
    CustomMessage, DialogOptions, ExecOptions, ExecResult, ModelEventStream, ModelRegistry,
    RemoteComponent, RemoteComponentInvalidate, RemoteComponentResult, SendMessageOptions,
    SourceInfo, TerminalInputResult, TerminalInputSubscription, ToolInfo, message_role,
    message_text,
};
pub use events::*;
#[doc(hidden)]
pub use extension::report_load_failure;
pub use extension::{
    CommandResult, Extension, Factory, FlagOptions, FlagType, MarkdownTransformContext, ProjectTrustDecision, ProjectTrustResult, ToolDefinition,
    ToolHandler, ToolPrepareArguments, ToolResult,
};
pub use login::LoginDefinition;
pub use oauth::{
    OAUTH_CANCELLED, OAuthAuthInfo, OAuthCredentialStatus, OAuthCredentialStore, OAuthCredentials,
    OAuthDeviceCodeInfo, OAuthGetApiKeyFn, OAuthLoginCallbacks, OAuthLoginFn, OAuthPrompt,
    OAuthProvider, OAuthRefreshFn, OAuthSelectOption, OAuthSelectPrompt,
};
pub use protocol::{AutocompleteItem, ConstrainedSampling, Schema, empty_schema};
pub use theme::{Theme, ThemeColorFn};
pub use tool_render::{
    ToolRenderCallHandler, ToolRenderContext, ToolRenderResult, ToolRenderResultHandler,
    ToolRenderResultOptions, ToolRenderShell,
};
