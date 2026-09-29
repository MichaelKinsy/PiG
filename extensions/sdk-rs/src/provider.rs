mod proxy;
#[cfg(all(test, unix))]
#[path = "provider_stream_shutdown_tests.rs"]
mod stream_shutdown_tests;

use crate::context::ModelEventStream;
use crate::extension::RequestThreads;
use crate::protocol::{CallMsg, Connection, Envelope, ProviderDef, RequestMsg};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex, Weak};

pub type ProviderModel = Arc<Value>;
pub type ProviderResult<T> = Result<T, String>;
pub type ProviderStreamFn = Arc<
    dyn Fn(ProviderModel, Value, ProviderStreamOptions) -> ProviderResult<Arc<ModelEventStream>>
        + Send
        + Sync,
>;
pub type ProviderFilterFn = Arc<
    dyn Fn(&[ProviderModel], Option<Value>) -> ProviderResult<Vec<ProviderModel>> + Send + Sync,
>;
type Callback = Arc<dyn Fn(Value) -> ProviderResult<Value> + Send + Sync>;
type Callbacks = HashMap<String, Callback>;

#[derive(Default)]
struct SignalState {
    cancelled: bool,
    next: u64,
    listeners: HashMap<u64, Arc<dyn Fn() + Send + Sync>>,
}
#[derive(Clone, Default)]
pub struct ProviderSignal {
    inner: Arc<(Mutex<SignalState>, Condvar)>,
}
pub struct ProviderSignalSubscription {
    signal: Weak<(Mutex<SignalState>, Condvar)>,
    id: u64,
}
impl Drop for ProviderSignalSubscription {
    fn drop(&mut self) {
        if let Some(signal) = self.signal.upgrade() {
            signal.0.lock().unwrap().listeners.remove(&self.id);
        }
    }
}
impl ProviderSignal {
    pub fn new() -> Self {
        Self::default()
    }
    pub fn is_cancelled(&self) -> bool {
        self.inner.0.lock().unwrap().cancelled
    }
    pub fn cancel(&self) {
        let callbacks = {
            let mut state = self.inner.0.lock().unwrap();
            if state.cancelled {
                return;
            }
            state.cancelled = true;
            self.inner.1.notify_all();
            state
                .listeners
                .drain()
                .map(|(_, callback)| callback)
                .collect::<Vec<_>>()
        };
        for callback in callbacks {
            callback()
        }
    }
    pub fn on_cancel(&self, callback: Arc<dyn Fn() + Send + Sync>) {
        let cancelled = {
            let mut state = self.inner.0.lock().unwrap();
            if !state.cancelled {
                state.next += 1;
                let id = state.next;
                state.listeners.insert(id, callback.clone());
                false
            } else {
                true
            }
        };
        if cancelled {
            callback()
        }
    }
    pub fn wait(&self) {
        let state = self.inner.0.lock().unwrap();
        drop(self.inner.1.wait_while(state, |s| !s.cancelled).unwrap());
    }
    pub fn subscribe(&self, callback: Arc<dyn Fn() + Send + Sync>) -> ProviderSignalSubscription {
        let (id, cancelled) = {
            let mut state = self.inner.0.lock().unwrap();
            state.next += 1;
            let id = state.next;
            state.listeners.insert(id, callback.clone());
            (id, state.cancelled)
        };
        if cancelled {
            callback()
        }
        ProviderSignalSubscription {
            signal: Arc::downgrade(&self.inner),
            id,
        }
    }
}

#[derive(Clone)]
pub struct AuthContext {
    pub env: Arc<dyn Fn(String) -> ProviderResult<Option<String>> + Send + Sync>,
    pub file_exists: Arc<dyn Fn(String) -> ProviderResult<bool> + Send + Sync>,
}
#[derive(Clone)]
pub struct APIKeyAuthInput {
    pub ctx: AuthContext,
    pub credential: Option<Value>,
    pub signal: ProviderSignal,
}
#[derive(Clone, Serialize, Deserialize)]
pub struct AuthResult {
    pub auth: Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub env: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
}
#[derive(Clone, Serialize, Deserialize)]
pub struct AuthCheck {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
}
#[derive(Clone)]
pub struct AuthInteraction {
    pub signal: ProviderSignal,
    pub prompt: Arc<dyn Fn(Value) -> ProviderResult<String> + Send + Sync>,
    pub notify: Arc<dyn Fn(Value) -> ProviderResult<()> + Send + Sync>,
}
pub struct APIKeyAuth {
    pub name: String,
    pub check:
        Option<Arc<dyn Fn(APIKeyAuthInput) -> ProviderResult<Option<AuthCheck>> + Send + Sync>>,
    pub resolve: Arc<dyn Fn(APIKeyAuthInput) -> ProviderResult<Option<AuthResult>> + Send + Sync>,
    pub login: Option<Arc<dyn Fn(AuthInteraction) -> ProviderResult<Value> + Send + Sync>>,
}
pub struct OAuthAuth {
    pub name: String,
    pub is_subscription: Option<bool>,
    pub login_label: Option<String>,
    pub login: Arc<dyn Fn(AuthInteraction) -> ProviderResult<Value> + Send + Sync>,
    pub refresh: Arc<dyn Fn(Value, ProviderSignal) -> ProviderResult<Value> + Send + Sync>,
    pub to_auth: Arc<dyn Fn(Value) -> ProviderResult<Value> + Send + Sync>,
}
#[derive(Default)]
pub struct ProviderAuth {
    pub api_key: Option<APIKeyAuth>,
    pub oauth: Option<OAuthAuth>,
}
#[derive(Clone, Default)]
pub struct ProviderStreamOptions {
    pub signal: ProviderSignal,
    pub values: Value,
    pub on_payload:
        Option<Arc<dyn Fn(Value, ProviderModel) -> ProviderResult<Value> + Send + Sync>>,
    pub on_response: Option<Arc<dyn Fn(Value, ProviderModel) -> ProviderResult<()> + Send + Sync>>,
    pub transform_headers: Option<Arc<dyn Fn(Value) -> ProviderResult<Value> + Send + Sync>>,
}
pub struct ModelsPublication {
    pub persist: Option<Value>,
    pub update: Option<Arc<dyn Fn() -> ProviderResult<()> + Send + Sync>>,
}
pub struct RefreshModelsContext {
    pub credential: Option<Value>,
    pub stored: Option<Value>,
    pub allow_network: bool,
    pub force: Option<bool>,
    pub signal: ProviderSignal,
    pub publish: Arc<dyn Fn(ModelsPublication) -> ProviderResult<bool> + Send + Sync>,
}
pub struct Provider {
    pub id: String,
    pub name: String,
    pub base_url: Option<String>,
    pub headers: Option<Value>,
    pub auth: ProviderAuth,
    pub get_models: Arc<dyn Fn() -> ProviderResult<Vec<ProviderModel>> + Send + Sync>,
    pub filter_models: Option<ProviderFilterFn>,
    pub refresh_models:
        Option<Arc<dyn Fn(RefreshModelsContext) -> ProviderResult<()> + Send + Sync>>,
    pub stream: ProviderStreamFn,
    pub stream_simple: ProviderStreamFn,
    pub fetch_deferred: Option<ProviderStreamFn>,
    pub cancel_deferred: Option<
        Arc<
            dyn Fn(ProviderModel, Value, ProviderStreamOptions) -> ProviderResult<()> + Send + Sync,
        >,
    >,
}

#[derive(Default)]
pub(crate) struct ProviderObjects {
    native: Mutex<HashMap<String, Arc<Provider>>>,
    proxies: Mutex<HashMap<String, (String, Weak<Provider>)>>,
    callbacks: Mutex<HashMap<String, Callbacks>>,
    updates: Mutex<HashMap<String, Arc<dyn Fn() -> ProviderResult<()> + Send + Sync>>>,
    pub(crate) streams: crate::context::ModelStreams,
    pub(crate) workers: Arc<RequestThreads>,
}
impl ProviderObjects {
    fn key(&self) -> String {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        format!(
            "provider-{}-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_nanos(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        )
    }
    pub(crate) fn release(&self, key: &str) {
        self.native.lock().unwrap().remove(key);
    }
    pub(crate) fn clear(&self) {
        self.native.lock().unwrap().clear();
        self.proxies.lock().unwrap().clear();
        self.callbacks.lock().unwrap().clear();
        self.updates.lock().unwrap().clear();
    }
    pub(crate) fn register(&self, provider: Arc<Provider>) -> ProviderResult<ProviderDef> {
        if provider.id.trim().is_empty() {
            return Err("Provider id must not be empty".into());
        }
        let key = self.key();
        let mut methods = vec!["getModels", "stream", "streamSimple"];
        for (method, present) in [
            ("filterModels", provider.filter_models.is_some()),
            ("refreshModels", provider.refresh_models.is_some()),
            ("fetchDeferred", provider.fetch_deferred.is_some()),
            ("cancelDeferred", provider.cancel_deferred.is_some()),
        ] {
            if present {
                methods.push(method)
            }
        }
        let mut auth = json!({});
        let mut oauth = Value::Null;
        if let Some(method) = &provider.auth.api_key {
            auth["apiKey"] = json!({"name":method.name});
            methods.push("auth.apiKey.resolve");
            if method.check.is_some() {
                methods.push("auth.apiKey.check")
            }
            if method.login.is_some() {
                methods.push("auth.apiKey.login")
            }
        }
        if let Some(method) = &provider.auth.oauth {
            auth["oauth"] = json!({"name":method.name});
            if let Some(value) = method.is_subscription {
                auth["oauth"]["isSubscription"] = json!(value)
            }
            if let Some(value) = &method.login_label {
                auth["oauth"]["loginLabel"] = json!(value)
            }
            oauth = json!({"name":method.name,"isSubscription":method.is_subscription.unwrap_or(false),"has_login":true,"has_refresh":true,"has_get_api_key":true});
            methods.extend([
                "auth.oauth.login",
                "auth.oauth.refresh",
                "auth.oauth.toAuth",
            ]);
        }
        let models = (provider.get_models)().unwrap_or_default();
        let mut native = json!({"id":provider.id,"key":key,"name":provider.name,"auth":auth,"models":models.iter().map(|m|m.as_ref()).collect::<Vec<_>>(),"methods":methods});
        if let Some(value) = &provider.base_url {
            native["baseUrl"] = json!(value)
        }
        if let Some(value) = &provider.headers {
            native["headers"] = value.clone()
        }
        if !oauth.is_null() {
            native["oauth"] = oauth
        }
        self.native.lock().unwrap().insert(key, provider.clone());
        Ok(ProviderDef {
            name: provider.id.clone(),
            config: json!({}),
            native: Some(native),
            stream_simple: false,
        })
    }
    pub(crate) fn dispatch_callback(&self, request: &RequestMsg) -> ProviderResult<Value> {
        let args = request.args.as_ref().ok_or("missing callback args")?;
        let callback = self
            .callbacks
            .lock()
            .unwrap()
            .get(request.tool.as_deref().unwrap_or_default())
            .and_then(|v| v.get(args["method"].as_str().unwrap_or_default()))
            .cloned()
            .ok_or("Provider callback is no longer active")?;
        callback(args["params"].clone())
    }
    pub(crate) fn dispatch(
        self: &Arc<Self>,
        conn: &Arc<Connection>,
        id: &str,
        request: &RequestMsg,
        signal: ProviderSignal,
    ) -> ProviderResult<Value> {
        let args = request.args.as_ref().ok_or("missing Provider args")?;
        let method = args["method"].as_str().ok_or("missing Provider method")?;
        let params = &args["params"];
        if method == "update" {
            let update = self
                .updates
                .lock()
                .unwrap()
                .get(params["token"].as_str().unwrap_or_default())
                .cloned()
                .ok_or("Provider publication is no longer active")?;
            update()?;
            return Ok(Value::Null);
        }
        let key = request.tool.as_deref().unwrap_or_default();
        let provider = self
            .native
            .lock()
            .unwrap()
            .get(key)
            .cloned()
            .ok_or("Provider object is no longer registered")?;
        let callback: Arc<dyn Fn(&str, Value) -> ProviderResult<Value> + Send + Sync> = {
            let conn = conn.clone();
            let parent = id.to_owned();
            let key = key.to_owned();
            Arc::new(move |method, params| {
                call_value(
                    &conn,
                    Some(&parent),
                    "provider.callback",
                    json!({"provider":key,"method":method,"params":params}),
                )
            })
        };
        let input = APIKeyAuthInput {
            credential: optional(&params["credential"]),
            signal: signal.clone(),
            ctx: AuthContext {
                env: {
                    let callback = callback.clone();
                    Arc::new(move |name| {
                        serde_json::from_value(callback("env", json!({"name":name}))?)
                            .map_err(|e| e.to_string())
                    })
                },
                file_exists: {
                    let callback = callback.clone();
                    Arc::new(move |path| {
                        serde_json::from_value(callback("fileExists", json!({"path":path}))?)
                            .map_err(|e| e.to_string())
                    })
                },
            },
        };
        let interaction = AuthInteraction {
            signal: signal.clone(),
            prompt: {
                let callback = callback.clone();
                Arc::new(move |prompt| {
                    serde_json::from_value(callback("prompt", json!({"prompt":prompt}))?)
                        .map_err(|e| e.to_string())
                })
            },
            notify: {
                let callback = callback.clone();
                Arc::new(move |event| {
                    callback("notify", json!({"event":event}))?;
                    Ok(())
                })
            },
        };
        match method {
            "getModels" => Ok(json!(
                (provider.get_models)()?
                    .iter()
                    .map(|m| m.as_ref())
                    .collect::<Vec<_>>()
            )),
            "filterModels" => {
                let models = params["models"]
                    .as_array()
                    .ok_or("models must be an array")?
                    .iter()
                    .cloned()
                    .map(Arc::new)
                    .collect::<Vec<_>>();
                let result = provider
                    .filter_models
                    .as_ref()
                    .ok_or("filterModels absent")?(
                    &models, optional(&params["credential"])
                )?;
                let indices = result
                    .iter()
                    .map(|model| {
                        models
                            .iter()
                            .position(|input| Arc::ptr_eq(model, input))
                            .map(|i| i as i64)
                            .unwrap_or(-1)
                    })
                    .collect::<Vec<_>>();
                Ok(
                    json!({"models":result.iter().map(|m|m.as_ref()).collect::<Vec<_>>(),"indices":indices}),
                )
            }
            "auth.apiKey.check" => {
                serde_json::to_value(provider
                    .auth
                    .api_key
                    .as_ref()
                    .ok_or("apiKey absent")?
                    .check
                    .as_ref()
                    .ok_or("check absent")?(input)?)
                .map_err(|e| e.to_string())
            }
            "auth.apiKey.resolve" => serde_json::to_value((provider
                .auth
                .api_key
                .as_ref()
                .ok_or("apiKey absent")?
                .resolve)(input)?)
            .map_err(|e| e.to_string()),
            "auth.apiKey.login" => provider
                .auth
                .api_key
                .as_ref()
                .ok_or("apiKey absent")?
                .login
                .as_ref()
                .ok_or("login absent")?(interaction),
            "auth.oauth.login" => {
                (provider.auth.oauth.as_ref().ok_or("oauth absent")?.login)(interaction)
            }
            "auth.oauth.refresh" => (provider.auth.oauth.as_ref().ok_or("oauth absent")?.refresh)(
                params["credential"].clone(),
                signal,
            ),
            "auth.oauth.toAuth" => (provider.auth.oauth.as_ref().ok_or("oauth absent")?.to_auth)(
                params["credential"].clone(),
            ),
            "refreshModels" => {
                let state = self.clone();
                let publish = Arc::new(move |publication: ModelsPublication| {
                    let token = state.key();
                    let mut value = json!({"publication":{}});
                    if let Some(persist) = publication.persist {
                        value["publication"]["persist"] = persist
                    }
                    if let Some(update) = publication.update {
                        state.updates.lock().unwrap().insert(token.clone(), update);
                        value["token"] = json!(token)
                    }
                    let result = callback("publish", value);
                    state.updates.lock().unwrap().remove(&token);
                    serde_json::from_value(result?).map_err(|e| e.to_string())
                });
                provider
                    .refresh_models
                    .as_ref()
                    .ok_or("refreshModels absent")?(RefreshModelsContext {
                    credential: optional(&params["credential"]),
                    stored: optional(&params["stored"]),
                    allow_network: params["allowNetwork"].as_bool().unwrap_or(false),
                    force: params["force"].as_bool(),
                    signal,
                    publish,
                })?;
                Ok(Value::Null)
            }
            "stream" | "streamSimple" | "fetchDeferred" | "cancelDeferred" => {
                if params["aborted"] == true {
                    signal.cancel()
                }
                let mut options = ProviderStreamOptions {
                    values: params["options"].clone(),
                    signal,
                    ..Default::default()
                };
                let flags = params["callbacks"].as_array().cloned().unwrap_or_default();
                if flags.contains(&json!("onPayload")) {
                    let callback = callback.clone();
                    options.on_payload = Some(Arc::new(move |value, model| {
                        callback("onPayload", json!({"value":value,"model":model.as_ref()}))
                    }))
                }
                if flags.contains(&json!("onResponse")) {
                    let callback = callback.clone();
                    options.on_response = Some(Arc::new(move |value, model| {
                        callback("onResponse", json!({"value":value,"model":model.as_ref()}))?;
                        Ok(())
                    }))
                }
                if flags.contains(&json!("transformHeaders")) {
                    let callback = callback.clone();
                    options.transform_headers = Some(Arc::new(move |value| {
                        callback("transformHeaders", json!({"value":value}))
                    }))
                }
                let model = Arc::new(params["model"].clone());
                if method == "cancelDeferred" {
                    provider
                        .cancel_deferred
                        .as_ref()
                        .ok_or("cancelDeferred absent")?(
                        model, params["handle"].clone(), options
                    )?;
                    return Ok(Value::Null);
                }
                let implementation = match method {
                    "stream" => &provider.stream,
                    "streamSimple" => &provider.stream_simple,
                    _ => provider
                        .fetch_deferred
                        .as_ref()
                        .ok_or("fetchDeferred absent")?,
                };
                let stream = implementation(
                    model,
                    if method == "fetchDeferred" {
                        params["handle"].clone()
                    } else {
                        params["context"].clone()
                    },
                    options,
                )?;
                conn.notify(
                    "tool_update",
                    Some(json!({"request_id":id,"result":{"type":"provider_started"}})),
                )
                .map_err(|e| e.to_string())?;
                stream.forward(&conn.closed_signal, |event| {
                    conn.notify("tool_update", Some(json!({"request_id":id,"result":event})))
                        .map_err(|error| error.to_string())
                })?;
                Ok(Value::Null)
            }
            _ => Err(format!("Unknown Provider method {method}")),
        }
    }
}
fn optional(value: &Value) -> Option<Value> {
    (!value.is_null()).then(|| value.clone())
}
fn call_value(
    conn: &Connection,
    parent: Option<&str>,
    method: &str,
    args: Value,
) -> ProviderResult<Value> {
    let result = conn
        .call_for(parent, method, Some(args))
        .map_err(|e| e.to_string())?;
    result_value(result)
}
fn result_value(result: crate::protocol::CallResultMsg) -> ProviderResult<Value> {
    if let Some(error) = result.error {
        Err(error.message)
    } else {
        Ok(result.result.unwrap_or(Value::Null))
    }
}
