//! OAuth provider bridge for the Rust SDK. Mirrors the Go SDK (`extensions/sdk/
//! oauth.go`) and the host wire contract (`coding/extension/host/subprocess/
//! protocol.go`): an extension attaches an [`OAuthProvider`] to a provider via
//! [`Extension::register_oauth_provider`](crate::Extension::register_oauth_provider);
//! the host RPCs `oauth_*` requests into these closures, and a login closure
//! drives the host UI through [`OAuthLoginCallbacks`] (`oauth.cb.*` calls).

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::protocol::Connection;

/// Returned by a value-returning login callback when the user dismissed the
/// host prompt.
pub const OAUTH_CANCELLED: &str = "oauth prompt cancelled";

/// A set of OAuth credentials. Field names are the wire shape shared with the
/// host and core.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OAuthCredentials {
    #[serde(default)]
    pub refresh: String,
    #[serde(default)]
    pub access: String,
    #[serde(default)]
    pub expires: i64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub project_id: String,
}

/// An authorization URL to present during login.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OAuthAuthInfo {
    pub url: String,
    #[serde(default)]
    pub instructions: String,
}

/// Device-code details to display during login.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OAuthDeviceCodeInfo {
    pub user_code: String,
    pub verification_uri: String,
    #[serde(default)]
    pub interval_seconds: f64,
    #[serde(default)]
    pub expires_in_seconds: f64,
}

/// A free-text prompt to show the user during login.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OAuthPrompt {
    pub message: String,
    #[serde(default)]
    pub placeholder: String,
    #[serde(default)]
    pub allow_empty: bool,
}

/// A single option in an [`OAuthSelectPrompt`].
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct OAuthSelectOption {
    pub id: String,
    pub label: String,
}

/// A choice prompt to show the user during login.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct OAuthSelectPrompt {
    pub message: String,
    pub options: Vec<OAuthSelectOption>,
}

/// Stored-credential status for a provider owning its own store.
#[derive(Clone, Debug, Default)]
pub struct OAuthCredentialStatus {
    pub present: bool,
    pub auth_type: String,
    pub source: String,
}

/// Lets a provider own credential persistence instead of core's `auth.json`.
pub trait OAuthCredentialStore: Send + Sync {
    fn credential_status(&self) -> OAuthCredentialStatus;
    fn store_credentials(&self, creds: OAuthCredentials) -> Result<String, String>;
    fn delete_credentials(&self) -> Result<bool, String>;
}

/// The login closure: runs the interactive flow, driving the host UI through
/// `cb`, and returns the resulting credentials.
pub type OAuthLoginFn =
    Box<dyn Fn(&OAuthLoginCallbacks) -> Result<OAuthCredentials, String> + Send + Sync>;
/// Exchanges refresh credentials for fresh ones.
pub type OAuthRefreshFn =
    Box<dyn Fn(OAuthCredentials) -> Result<OAuthCredentials, String> + Send + Sync>;
/// Resolves the bearer to send for a set of credentials.
pub type OAuthGetApiKeyFn = Box<dyn Fn(OAuthCredentials) -> String + Send + Sync>;

/// An OAuth capability attached to a model provider. Only the non-`None`
/// closures are advertised as capabilities to the host.
pub struct OAuthProvider {
    /// Informational; the registry key is the provider name passed to
    /// `register_oauth_provider`. Empty defaults to that name.
    pub name: String,
    /// Whether access through this OAuth method is subscription-backed.
    pub is_subscription: bool,
    /// Runs the interactive login flow. Required.
    pub login: OAuthLoginFn,
    /// Refreshes credentials. Optional.
    pub refresh_token: Option<OAuthRefreshFn>,
    /// Resolves the bearer for a set of credentials. Optional; when absent the
    /// access token is used directly.
    pub get_api_key: Option<OAuthGetApiKeyFn>,
    /// When set, the provider owns credential persistence.
    pub credential_store: Option<Box<dyn OAuthCredentialStore>>,
}

/// The serializable capability descriptor placed under the "oauth" key of a
/// provider config on the wire. Matches the host `ProviderOAuthConfig`.
#[derive(Serialize)]
pub(crate) struct ProviderOAuthConfig {
    pub name: String,
    #[serde(rename = "isSubscription", skip_serializing_if = "is_false")]
    pub is_subscription: bool,
    pub has_login: bool,
    pub has_refresh: bool,
    pub has_get_api_key: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub has_credential_store: bool,
}

fn is_false(b: &bool) -> bool {
    !*b
}

impl ProviderOAuthConfig {
    pub(crate) fn from_provider(name: &str, provider: &OAuthProvider) -> Self {
        let declared = if provider.name.is_empty() {
            name.to_string()
        } else {
            provider.name.clone()
        };
        Self {
            name: declared,
            is_subscription: provider.is_subscription,
            has_login: true,
            has_refresh: provider.refresh_token.is_some(),
            has_get_api_key: provider.get_api_key.is_some(),
            has_credential_store: provider.credential_store.is_some(),
        }
    }
}

#[derive(Deserialize, Default)]
struct OAuthInputResult {
    #[serde(default)]
    value: String,
    #[serde(default)]
    cancel: bool,
}

/// Drives the host login UI from inside a provider's login closure. Each method
/// issues an `oauth.cb.*` call to the host; value-returning methods block until
/// the user responds.
pub struct OAuthLoginCallbacks {
    pub(crate) conn: Arc<Connection>,
    pub(crate) request_id: String,
}

impl OAuthLoginCallbacks {
    /// Report an authorization URL to open. Fire-and-forget.
    pub fn on_auth(&self, info: OAuthAuthInfo) {
        let _ = self.conn.call_for(
            Some(&self.request_id),
            "oauth.cb.onAuth",
            serde_json::to_value(info).ok(),
        );
        let _ = self.conn.request_state(&self.request_id, "progress", None);
    }

    /// Report device-code details to display. Fire-and-forget.
    pub fn on_device_code(&self, info: OAuthDeviceCodeInfo) {
        let _ = self.conn.call_for(
            Some(&self.request_id),
            "oauth.cb.onDeviceCode",
            serde_json::to_value(info).ok(),
        );
        let _ = self.conn.request_state(&self.request_id, "progress", None);
    }

    /// Report a status line during login. Fire-and-forget.
    pub fn on_progress(&self, message: &str) {
        let _ = self.conn.call_for(
            Some(&self.request_id),
            "oauth.cb.onProgress",
            Some(json!({ "message": message })),
        );
        let _ = self.conn.request_state(&self.request_id, "progress", None);
    }

    /// Ask the user for free-text input. Returns [`OAUTH_CANCELLED`] as the
    /// error when the user dismissed the prompt.
    pub fn on_prompt(&self, prompt: OAuthPrompt) -> Result<String, String> {
        self.input_call("oauth.cb.onPrompt", serde_json::to_value(prompt).ok())
    }

    /// Ask the user to choose an option.
    pub fn on_select(&self, prompt: OAuthSelectPrompt) -> Result<String, String> {
        self.input_call("oauth.cb.onSelect", serde_json::to_value(prompt).ok())
    }

    /// Ask the user to paste a code.
    pub fn on_manual_code_input(&self) -> Result<String, String> {
        self.input_call("oauth.cb.onManualCodeInput", None)
    }

    fn input_call(&self, method: &str, args: Option<serde_json::Value>) -> Result<String, String> {
        let _ = self
            .conn
            .request_state(&self.request_id, "blocked", Some("user"));
        let result = self
            .conn
            .call_for(Some(&self.request_id), method, args)
            .map_err(|e| e.to_string())?;
        let _ = self.conn.request_state(&self.request_id, "progress", None);
        if let Some(err) = result.error {
            return Err(err.message);
        }
        let input: OAuthInputResult = result
            .result
            .and_then(|v| serde_json::from_value(v).ok())
            .unwrap_or_default();
        if input.cancel {
            return Err(OAUTH_CANCELLED.to_string());
        }
        Ok(input.value)
    }
}
