//! Pi's `ModelRuntime` class (core/model-runtime.ts): the configured model collection an extension builds over its own credential and model files, for models, provider auth, completions and the registrations a Session shares. The host owns the object for the extension's connection; this value names it. Models are Pi's Model objects as JSON and every result is Pi's in the JSON shape of Pi's types.
//!
//! The Provider objects of Pi's class (`getProviders`, `getProvider`, `getRegisteredNativeProvider`, `registerNativeProvider`) are not here: a Provider holds functions the host cannot run on the extension's behalf (the Provider surface D78 records). `register_provider` takes a configuration without functions. `streamDeferred`, `fetchDeferred` and `cancelDeferred` are not here either.

use crate::context::{Context, ModelEventStream};
use crate::graph::{callback_id, GraphCallback, GraphObject};
use serde_json::{json, Value};
use std::io;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

/// Pi's `CreateModelRuntimeOptions`. `None` and `false` members are Pi's undefined. The credential store option of Pi (an object of the extension's own) is not served: `auth_path` names the file.
#[derive(Debug, Clone, Default)]
pub struct ModelRuntimeCreateOptions {
    /// The credential file; `None` is auth.json in the agent directory.
    pub auth_path: Option<String>,
    /// The models.json file; `None` is models.json in the agent directory.
    pub models_path: Option<String>,
    /// Pi's `modelsPath: null`: no models.json, and an in-memory models store.
    pub no_models_file: bool,
    /// The models store file; `None` is models-store.json next to the models file.
    pub models_store_path: Option<String>,
    /// Lets create refresh model catalogs over the network.
    pub allow_model_network: bool,
    pub model_refresh_timeout_ms: Option<u64>,
    pub catalog_base_url: Option<String>,
    /// `Some(false)` skips the initial refresh.
    pub refresh_on_create: Option<bool>,
}

/// Pi's `ModelRuntime`. Dropping it releases the host's reference.
pub struct ModelRuntime {
    object: GraphObject,
}

fn provider_arg(provider: Option<&str>) -> Value {
    provider.map_or(Value::Null, |provider| json!(provider))
}

fn never_fails(context: &Context, model: &Value, field: &str, empty: Value, outcome: io::Result<Value>) -> Value {
    match outcome {
        Ok(result) => result,
        Err(error) => {
            let aborted = context.request_parent.as_ref().is_some_and(|parent| parent.cancelled());
            let timestamp = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_millis() as u64;
            let mut result = json!({
                "api": model["api"], "provider": model["provider"], "model": model["id"],
                "stopReason": if aborted { "aborted" } else { "error" }, "errorMessage": error.to_string(), "timestamp": timestamp
            });
            result[field] = empty;
            result
        }
    }
}

impl ModelRuntime {
    /// `ModelRuntime.create(options)`.
    pub fn create(context: &Context, options: ModelRuntimeCreateOptions) -> io::Result<ModelRuntime> {
        let mut wire = serde_json::Map::new();
        if let Some(path) = &options.auth_path {
            wire.insert("authPath".into(), json!(path));
        }
        if options.no_models_file {
            wire.insert("modelsPath".into(), Value::Null);
        } else if let Some(path) = &options.models_path {
            wire.insert("modelsPath".into(), json!(path));
        }
        if let Some(path) = &options.models_store_path {
            wire.insert("modelsStorePath".into(), json!(path));
        }
        if options.allow_model_network {
            wire.insert("allowModelNetwork".into(), json!(true));
        }
        if let Some(timeout) = options.model_refresh_timeout_ms {
            wire.insert("modelRefreshTimeoutMs".into(), json!(timeout));
        }
        if let Some(url) = &options.catalog_base_url {
            wire.insert("catalogBaseUrl".into(), json!(url));
        }
        if let Some(refresh) = options.refresh_on_create {
            wire.insert("refreshOnCreate".into(), json!(refresh));
        }
        Ok(ModelRuntime { object: GraphObject::construct(context, "ModelRuntime", "create", vec![Value::Object(wire)])? })
    }

    /// The host's name for the runtime, which an option that takes the runtime (`create_agent_session`) puts on the wire.
    pub fn handle(&self) -> &str {
        self.object.handle()
    }

    pub fn get_models(&self, provider: Option<&str>) -> io::Result<Value> {
        self.object.call("getModels", vec![provider_arg(provider)])
    }

    pub fn get_model(&self, provider: &str, model_id: &str) -> io::Result<Value> {
        self.object.call("getModel", vec![json!(provider), json!(model_id)])
    }

    /// A catalog chat model that is not virtual, null when there is none.
    pub fn get_physical_model(&self, provider: &str, model_id: &str) -> io::Result<Value> {
        self.object.call("getPhysicalModel", vec![json!(provider), json!(model_id)])
    }

    pub fn get_models_of_type(&self, model_type: &str, provider: Option<&str>) -> io::Result<Value> {
        self.object.call("getModelsOfType", vec![json!(model_type), provider_arg(provider)])
    }

    pub fn get_model_of_type(&self, model_type: &str, provider: &str, model_id: &str) -> io::Result<Value> {
        self.object.call("getModelOfType", vec![json!(model_type), json!(provider), json!(model_id)])
    }

    pub fn get_all_models(&self, provider: Option<&str>) -> io::Result<Value> {
        self.object.call("getAllModels", vec![provider_arg(provider)])
    }

    pub fn get_available_of_type(&self, model_type: &str, provider: Option<&str>) -> io::Result<Value> {
        self.object.call_async("getAvailableOfType", vec![json!(model_type), provider_arg(provider)])
    }

    pub fn get_all_available(&self, provider: Option<&str>) -> io::Result<Value> {
        self.object.call_async("getAllAvailable", vec![provider_arg(provider)])
    }

    /// The available chat models after an availability refresh, or of one provider.
    pub fn get_available(&self, provider: Option<&str>) -> io::Result<Value> {
        self.object.call_async("getAvailable", vec![provider_arg(provider)])
    }

    /// The available chat models as of the last availability pass, without waiting.
    pub fn get_available_snapshot(&self) -> io::Result<Value> {
        self.object.call("getAvailableSnapshot", vec![])
    }

    pub fn get_error(&self) -> io::Result<Option<String>> {
        Ok(self.object.call("getError", vec![])?.as_str().map(str::to_string))
    }

    /// `{"type", "source"?}` of the credential the provider's auth resolves to, null when it is unconfigured.
    pub fn check_auth(&self, provider: &str) -> io::Result<Value> {
        self.object.call_async("checkAuth", vec![json!(provider)])
    }

    pub fn get_registered_provider_ids(&self) -> io::Result<Value> {
        self.object.call("getRegisteredProviderIds", vec![])
    }

    pub fn get_registered_provider_config(&self, provider: &str) -> io::Result<Value> {
        self.object.call("getRegisteredProviderConfig", vec![json!(provider)])
    }

    pub fn get_compatibility_request_config(&self, model: &Value) -> io::Result<Value> {
        self.object.call("getCompatibilityRequestConfig", vec![model.clone()])
    }

    pub fn is_using_oauth(&self, provider: &str) -> io::Result<bool> {
        Ok(self.object.call("isUsingOAuth", vec![json!(provider)])?.as_bool().unwrap_or(false))
    }

    pub fn is_using_subscription(&self, provider: &str) -> io::Result<bool> {
        Ok(self.object.call("isUsingSubscription", vec![json!(provider)])?.as_bool().unwrap_or(false))
    }

    pub fn has_configured_auth(&self, provider: &str) -> io::Result<bool> {
        Ok(self.object.call("hasConfiguredAuth", vec![json!(provider)])?.as_bool().unwrap_or(false))
    }

    pub fn get_provider_auth_status(&self, provider: &str) -> io::Result<Value> {
        self.object.call("getProviderAuthStatus", vec![json!(provider)])
    }

    /// Request auth for a provider id (a string) or a model object as Pi's AuthResult (`{"auth": {...}, "env"?, "source"?}`), null when it is unconfigured. `overrides` are Pi's `apiKey`, `env` and `minOAuthValidityMs`.
    pub fn get_auth(&self, provider_or_model: Value, overrides: Option<Value>) -> io::Result<Value> {
        if !provider_or_model.is_string() && !provider_or_model.is_object() {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "get_auth takes a provider id or a model object"));
        }
        self.object.call_async("getAuth", vec![provider_or_model, overrides.unwrap_or(Value::Null)])
    }

    pub fn set_runtime_api_key(&self, provider: &str, api_key: &str) -> io::Result<()> {
        self.object.call_async("setRuntimeApiKey", vec![json!(provider), json!(api_key)]).map(|_| ())
    }

    pub fn remove_runtime_api_key(&self, provider: &str) -> io::Result<()> {
        self.object.call_async("removeRuntimeApiKey", vec![json!(provider)]).map(|_| ())
    }

    pub fn list_credentials(&self) -> io::Result<Value> {
        self.object.call_async("listCredentials", vec![])
    }

    /// Authenticates `provider` with the auth method of `kind` (`"api_key"` or `"oauth"`); `prompt` answers Pi's login prompts and `notify` receives its events. Returns Pi's Credential.
    pub fn login(
        &self,
        provider: &str,
        kind: &str,
        prompt: Arc<dyn Fn(Value) -> Result<String, String> + Send + Sync>,
        notify: Option<Arc<dyn Fn(Value) + Send + Sync>>,
        agent_name: Option<&str>,
        get_device_id: Option<Arc<dyn Fn() -> String + Send + Sync>>,
    ) -> io::Result<Value> {
        let context = self.object.context();
        let prompt: GraphCallback = Arc::new(move |args: Vec<Value>| prompt(args.into_iter().next().unwrap_or(Value::Null)).map(Value::String));
        let notify: Option<GraphCallback> = notify.map(|notify| Arc::new(move |args: Vec<Value>| { notify(args.into_iter().next().unwrap_or(Value::Null)); Ok(Value::Null) }) as GraphCallback);
        let interaction = json!({"prompt": callback_id(context, Some(prompt)), "notify": callback_id(context, notify)});
        let device: Option<GraphCallback> = get_device_id.map(|device| Arc::new(move |_args: Vec<Value>| Ok(Value::String(device()))) as GraphCallback);
        let options = if agent_name.is_none() && device.is_none() { Value::Null } else { json!({"agentName": agent_name, "getDeviceId": callback_id(context, device)}) };
        self.object.call_async("login", vec![json!(provider), json!(kind), interaction, options])
    }

    pub fn logout(&self, provider: &str) -> io::Result<()> {
        self.object.call_async("logout", vec![json!(provider)]).map(|_| ())
    }

    /// `{"aborted", "errors"}`; `options` are Pi's `allowNetwork`, `providers` and `force`.
    pub fn refresh(&self, options: Option<Value>) -> io::Result<Value> {
        self.object.call_async("refresh", vec![options.unwrap_or_else(|| json!({}))])
    }

    /// Registers a provider by a configuration without functions (name, baseUrl, apiKey, api, headers, authHeader, models).
    pub fn register_provider(&self, provider: &str, config: Value) -> io::Result<()> {
        self.object.call("registerProvider", vec![json!(provider), config]).map(|_| ())
    }

    pub fn unregister_provider(&self, provider: &str) -> io::Result<()> {
        self.object.call("unregisterProvider", vec![json!(provider)]).map(|_| ())
    }

    /// Registers a virtual model; its route runs in this process when a request resolves the model.
    pub fn register_virtual_model(&self, model: crate::VirtualModel) -> io::Result<()> {
        let context = self.object.context().clone();
        let declaration = serde_json::to_value(model.declaration()).map_err(io::Error::other)?;
        let route_model = Arc::new(model);
        let route: GraphCallback = {
            let route_model = route_model.clone();
            Arc::new(move |args: Vec<Value>| {
                let request = serde_json::from_value(args.into_iter().next().unwrap_or(Value::Null)).map_err(|error| format!("decode the route request: {error}"))?;
                let routed = (route_model.route)(&context, request)?;
                serde_json::to_value(routed).map_err(|error| error.to_string())
            })
        };
        let mut definition = declaration;
        definition["route"] = callback_id(self.object.context(), Some(route));
        self.object.call("registerVirtualModel", vec![definition]).map(|_| ())
    }

    pub fn unregister_virtual_model(&self, provider: &str, id: &str) -> io::Result<()> {
        self.object.call("unregisterVirtualModel", vec![json!(provider), json!(id)]).map(|_| ())
    }

    /// Pi's ModelRoute (`{"model", "thinkingLevel", "state"?}`) of one request to a virtual model; `options` are `reason`, `thinkingLevel`, `failed` and `state`.
    pub fn resolve_model(&self, model: &Value, messages: Value, options: Value) -> io::Result<Value> {
        self.object.call_async("resolveModel", vec![model.clone(), messages, options])
    }

    /// Never fails: a failure is a result with `stopReason` `error` or `aborted`.
    pub fn classify(&self, model: &Value, context: Value, options: Option<Value>) -> Value {
        let outcome = self.object.call_async("classify", vec![model.clone(), context, options.unwrap_or(Value::Null)]);
        never_fails(self.object.context(), model, "answers", json!({}), outcome)
    }

    /// Never fails: a failure is a result with `stopReason` `error` or `aborted`.
    pub fn generate_images(&self, model: &Value, context: Value, options: Option<Value>) -> Value {
        let outcome = self.object.call_async("generateImages", vec![model.clone(), context, options.unwrap_or(Value::Null)]);
        never_fails(self.object.context(), model, "output", json!([]), outcome)
    }

    /// Streams a model response through the runtime (`request` and `options` as for `ModelRegistry::stream`).
    pub fn stream(&self, model: Value, request: Value, options: Value) -> Arc<ModelEventStream> {
        self.object.context().model_registry().stream_on_runtime(self.handle(), model, request, options, false)
    }

    pub fn stream_simple(&self, model: Value, request: Value, options: Value) -> Arc<ModelEventStream> {
        self.object.context().model_registry().stream_on_runtime(self.handle(), model, request, options, true)
    }

    pub fn complete(&self, model: Value, request: Value, options: Value) -> Option<Value> {
        self.stream(model, request, options).result()
    }

    pub fn complete_simple(&self, model: Value, request: Value, options: Value) -> Option<Value> {
        self.stream_simple(model, request, options).result()
    }
}
