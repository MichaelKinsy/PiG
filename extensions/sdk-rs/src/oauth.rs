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

/// Pi's OAuth token object (`packages/ai/src/auth/types.ts` `OAuthCredentials`). Field names are the wire
/// shape shared with the host and core.
///
/// `extra` retains every other provider-owned key, including a present empty or `null` value of a named
/// optional field. When the integer `expires` cannot represent Pi's value exactly (a fraction, an
/// out-of-range number or a non-number), `extra["expires"]` holds the exact value while `expires` equals
/// its truncated projection; assigning a different `expires` replaces it. `expires_absent` records a
/// credential without an `expires` property. Use [`OAuthCredentials::expires_millis`],
/// [`OAuthCredentials::set_expires_millis`] and [`OAuthCredentials::clear_expires`] instead of editing
/// these directly.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct OAuthCredentials {
    pub refresh: String,
    pub access: String,
    pub expires: i64,
    pub project_id: String,
    pub account_id: String,
    pub scope: String,
    pub extra: serde_json::Map<String, serde_json::Value>,
    pub expires_absent: bool,
}

/// Only a safe integer is its own `JSON.stringify`: a larger integer is written with `Number::toString` digits (2**60 is 1152921504606847000), which the `i64` encoding would not reproduce.
const MAX_SAFE_INTEGER: f64 = 9_007_199_254_740_992.0;

fn exact_i64(value: f64) -> Option<i64> {
    (value.trunc() == value && value.abs() <= MAX_SAFE_INTEGER).then_some(value as i64)
}

fn expires_projection(value: f64) -> i64 {
    if value.is_finite() && value >= -9_223_372_036_854_775_808.0 && value < 9_223_372_036_854_775_808.0 { value.trunc() as i64 } else { 0 }
}

fn exact_value_projection(value: &serde_json::Value) -> i64 {
    value.as_f64().map(expires_projection).unwrap_or(0)
}

/// JSON.stringify of a JavaScript number: non-finite values become `null`, `-0` becomes `0`.
fn js_number_value(value: f64) -> serde_json::Value {
    if !value.is_finite() {
        return serde_json::Value::Null;
    }
    if value == 0.0 {
        return json!(0);
    }
    serde_json::Number::from_f64(value).map(serde_json::Value::Number).unwrap_or(serde_json::Value::Null)
}

impl OAuthCredentials {
    fn exact_expires(&self) -> Option<&serde_json::Value> {
        self.extra.get("expires").filter(|value| exact_value_projection(value) == self.expires)
    }

    /// The exact `expires` number, or `None` when the property is absent or holds a value that Pi's
    /// declared number type excludes. Such a value still round-trips unchanged.
    pub fn expires_millis(&self) -> Option<f64> {
        if self.expires_absent && self.expires == 0 {
            return None;
        }
        match self.exact_expires() {
            Some(value) => value.as_f64(),
            None => Some(self.expires as f64),
        }
    }

    /// Whether the credential carries an `expires` property.
    pub fn has_expires(&self) -> bool {
        !(self.expires_absent && self.expires == 0)
    }

    /// Stores a JavaScript number, including a fraction; `expires` receives its truncated projection.
    pub fn set_expires_millis(&mut self, value: f64) {
        self.expires_absent = false;
        self.extra.remove("expires");
        match exact_i64(value) {
            Some(integer) => self.expires = integer,
            None => {
                self.expires = expires_projection(value);
                self.extra.insert("expires".into(), js_number_value(value));
            }
        }
    }

    /// Removes the `expires` property.
    pub fn clear_expires(&mut self) {
        self.expires = 0;
        self.expires_absent = true;
        self.extra.remove("expires");
    }
}

impl<'de> Deserialize<'de> for OAuthCredentials {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let mut object = serde_json::Map::<String, serde_json::Value>::deserialize(deserializer)?;
        let mut take_string = |key: &str, optional: bool| -> Result<String, D::Error> {
            match object.get(key) {
                Some(serde_json::Value::String(text)) if !optional || !text.is_empty() => {
                    let text = text.clone();
                    object.remove(key);
                    Ok(text)
                }
                Some(other) if !optional => Err(serde::de::Error::custom(format!("{key}: expected string, got {other}"))),
                _ => Ok(String::new()),
            }
        };
        let refresh = take_string("refresh", false)?;
        let access = take_string("access", false)?;
        let project_id = take_string("projectId", true)?;
        let account_id = take_string("accountId", true)?;
        let scope = take_string("scope", true)?;
        let mut credentials = OAuthCredentials { refresh, access, project_id, account_id, scope, ..Default::default() };
        match object.remove("expires") {
            None => credentials.expires_absent = true,
            Some(serde_json::Value::Number(number)) => {
                let value = number.as_f64().unwrap_or(f64::NAN);
                match exact_i64(value) {
                    Some(integer) => credentials.expires = integer,
                    None => {
                        credentials.expires = expires_projection(value);
                        object.insert("expires".into(), js_number_value(value));
                    }
                }
            }
            Some(other) => {
                object.insert("expires".into(), other);
            }
        }
        credentials.extra = object;
        Ok(credentials)
    }
}

impl Serialize for OAuthCredentials {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        let mut value = self.extra.clone();
        value.insert("refresh".into(), json!(self.refresh));
        value.insert("access".into(), json!(self.access));
        match self.exact_expires() {
            Some(_) => {}
            None if self.expires_absent && self.expires == 0 => {
                value.remove("expires");
            }
            None => {
                value.insert("expires".into(), json!(self.expires));
            }
        }
        for (key, text) in [("projectId", &self.project_id), ("accountId", &self.account_id), ("scope", &self.scope)] {
            if !text.is_empty() {
                value.insert(key.into(), json!(text));
            }
        }
        value.serialize(serializer)
    }
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

#[cfg(test)]
mod credential_tests {
    use super::OAuthCredentials;
    use serde_json::{Value, json};

    fn round_trip(input: Value) -> Value {
        let credentials: OAuthCredentials = serde_json::from_value(input).unwrap();
        serde_json::to_value(credentials).unwrap()
    }

    // Pi's OAuthCredentials is `{ refresh, access, expires: number, [key]: unknown }` (packages/ai/src/auth/types.ts:23-28).
    #[test]
    fn credentials_retain_exact_expiry_and_provider_keys() {
        let meta = json!({"k": [1, null, ""]});
        for (input, expires, millis) in [
            (json!({"refresh":"r","access":"a","expires":1_700_000_000_000.5_f64,"meta":meta,"projectId":""}), 1_700_000_000_000_i64, Some(1_700_000_000_000.5)),
            (json!({"refresh":"r","access":"a","expires":1e21,"meta":meta}), 0, Some(1e21)),
            (json!({"refresh":"r","access":"a","expires":"soon"}), 0, None),
            (json!({"refresh":"r","access":"a","expires":null}), 0, None),
            (json!({"refresh":"r","access":"a","expires":42}), 42, Some(42.0)),
        ] {
            let credentials: OAuthCredentials = serde_json::from_value(input.clone()).unwrap();
            assert_eq!(credentials.expires, expires, "{input}");
            assert_eq!(credentials.expires_millis(), millis, "{input}");
            assert!(credentials.has_expires());
            assert_eq!(round_trip(input.clone()), input);
        }
    }

    // JSON.parse reads 1152921504606847000 as the double 2**60 (packages/ai/src/auth/types.ts:24-27 declares expires a number); the Go SDK projects the same double, so the exact integer i64 must not survive.
    #[test]
    fn integer_expiry_beyond_two_to_the_53_is_a_javascript_number() {
        let two_60 = 1_152_921_504_606_846_976_i64;
        for text in ["1152921504606847000", "1152921504606846976", "1.152921504606847e18"] {
            let credentials: OAuthCredentials = serde_json::from_str(&format!(r#"{{"refresh":"r","access":"a","expires":{text}}}"#)).unwrap();
            assert_eq!(credentials.expires_millis(), Some(two_60 as f64), "{text}");
            assert_eq!(credentials.expires, two_60, "{text}");
            assert_eq!(serde_json::to_value(&credentials).unwrap()["expires"].as_f64(), Some(two_60 as f64), "{text}");
        }
        let safe: OAuthCredentials = serde_json::from_str(r#"{"refresh":"r","access":"a","expires":9007199254740992}"#).unwrap();
        assert_eq!(safe.expires_millis(), Some(9_007_199_254_740_992.0));
        assert!(safe.extra.get("expires").is_none());
        let mut assigned = OAuthCredentials::default();
        assigned.set_expires_millis(two_60 as f64);
        assert!(assigned.extra.get("expires").is_some());
        assert_eq!(assigned.expires_millis(), Some(two_60 as f64));
    }

    #[test]
    fn absent_expiry_stays_absent() {
        let input = json!({"refresh":"r","access":"a","type":"oauth"});
        let credentials: OAuthCredentials = serde_json::from_value(input.clone()).unwrap();
        assert!(!credentials.has_expires());
        assert_eq!(credentials.expires_millis(), None);
        assert_eq!(round_trip(input.clone()), input);
    }

    #[test]
    fn assigning_expires_replaces_the_retained_exact_value() {
        let mut credentials: OAuthCredentials = serde_json::from_value(json!({"refresh":"r","access":"a","expires":10.5})).unwrap();
        assert_eq!(credentials.expires_millis(), Some(10.5));
        credentials.expires = 99;
        assert_eq!(credentials.expires_millis(), Some(99.0));
        assert_eq!(serde_json::to_value(&credentials).unwrap()["expires"], json!(99));
        credentials.set_expires_millis(-0.0);
        assert_eq!(serde_json::to_value(&credentials).unwrap()["expires"], json!(0));
        credentials.set_expires_millis(2.25);
        assert_eq!((credentials.expires, credentials.expires_millis()), (2, Some(2.25)));
        credentials.clear_expires();
        assert!(serde_json::to_value(&credentials).unwrap().get("expires").is_none());
        let literal = OAuthCredentials { access: "x".into(), expires: 7, ..Default::default() };
        assert_eq!(serde_json::to_value(&literal).unwrap()["expires"], json!(7));
    }
}
