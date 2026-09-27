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

mod context;
mod extension;
mod login;
mod oauth;
mod protocol;
mod transport;

pub use context::{
    CommandInfo, Context, ModelEventStream, ModelRegistry, RemoteComponent,
    RemoteComponentInvalidate, RemoteComponentResult, SourceInfo, TerminalInputResult,
    TerminalInputSubscription, ToolInfo, message_role, message_text,
};
#[doc(hidden)]
pub use extension::report_load_failure;
pub use extension::{
    CommandResult, Extension, Factory, ProjectTrustDecision, ProjectTrustResult, ToolResult,
};
pub use login::LoginDefinition;
pub use oauth::{
    OAUTH_CANCELLED, OAuthAuthInfo, OAuthCredentialStatus, OAuthCredentialStore, OAuthCredentials,
    OAuthDeviceCodeInfo, OAuthGetApiKeyFn, OAuthLoginCallbacks, OAuthLoginFn, OAuthPrompt,
    OAuthProvider, OAuthRefreshFn, OAuthSelectOption, OAuthSelectPrompt,
};
pub use protocol::{ConstrainedSampling, Schema, empty_schema};
