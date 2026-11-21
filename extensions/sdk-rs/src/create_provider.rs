//! pi-ai's `createProvider` (packages/ai/src/models.ts:1077): builds a [`Provider`] from parts.
//!
//! A single API streams every chat model, a list of APIs dispatches on `model.api`, and a model whose api has no entry produces a stream error; one-shot operation lists dispatch on `model.api` the same way. The returned
//! Provider is the object `register_native_provider` takes. Pure and synchronous: it calls no host function.

use crate::context::{ModelEventStream, create_assistant_message_event_stream};
use crate::provider::{
    ModelsPublication, Provider, ProviderAuth, ProviderFilterFn, ProviderModel,
    ProviderOperationFn, ProviderOperationOptions, ProviderResult, ProviderStreamFn,
    ProviderStreamOptions, RefreshModelsContext,
};
use serde_json::{Value, json};
use std::collections::HashMap;
use std::sync::{Arc, RwLock};

/// A deferred-response cancellation: the model, the handle and the options.
pub type ProviderCancelDeferredFn =
    Arc<dyn Fn(ProviderModel, Value, ProviderStreamOptions) -> ProviderResult<()> + Send + Sync>;

/// pi-ai's `ProviderStreams`: the chat implementation of one API. `fetch_deferred` and `cancel_deferred` are optional.
#[derive(Clone)]
pub struct ProviderStreams {
    pub stream: ProviderStreamFn,
    pub stream_simple: ProviderStreamFn,
    pub fetch_deferred: Option<ProviderStreamFn>,
    pub cancel_deferred: Option<ProviderCancelDeferredFn>,
}

/// Pi's `api` option: one [`ProviderStreams`] for every chat model, or a list keyed by `model.api` (a later entry of the same API replaces an earlier one, as a repeated key of Pi's object does).
#[derive(Clone)]
pub enum ProviderApi {
    Single(ProviderStreams),
    ByApi(Vec<(String, ProviderStreams)>),
}

/// The function that fetches a dynamic model overlay of every type.
pub type FetchModelsFn =
    Arc<dyn Fn(&RefreshModelsContext) -> ProviderResult<Vec<Value>> + Send + Sync>;

/// pi-ai's `CreateProviderOptions`. `None` and empty lists are Pi's undefined. `auth` is required: every provider has auth semantics, even ambient or keyless ones. `models` is the static baseline of every type (a model
/// without `type` is a chat model). `fetch_models` fetches a dynamic model overlay of every type, which is restored and published transactionally; models of unknown types are dropped.
pub struct CreateProviderOptions {
    pub id: String,
    pub name: Option<String>,
    pub base_url: Option<String>,
    pub headers: Option<Value>,
    pub auth: ProviderAuth,
    pub models: Vec<Value>,
    pub fetch_models: Option<FetchModelsFn>,
    pub filter_models: Option<ProviderFilterFn>,
    pub filter_all_models: Option<ProviderFilterFn>,
    pub api: Option<ProviderApi>,
    pub images: Vec<(String, ProviderOperationFn)>,
    pub classifiers: Vec<(String, ProviderOperationFn)>,
}

fn model_type(model: &Value) -> &str {
    model["type"].as_str().unwrap_or("chat")
}

fn api_text(model: &Value) -> String {
    match &model["api"] {
        Value::Null => "undefined".to_string(),
        Value::String(text) => text.clone(),
        other => other.to_string(),
    }
}

fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

/// pi-ai's lazyStream (api/lazy.ts) of a setup that threw: the stream ends with an error event carrying the error message of an empty assistant message.
fn setup_error_stream(model: &Value, error: &str) -> Arc<ModelEventStream> {
    let message = json!({
        "role": "assistant", "content": [], "api": model["api"], "provider": model["provider"], "model": model["id"],
        "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
        "stopReason": "error", "errorMessage": error, "timestamp": now_ms(),
    });
    let stream = create_assistant_message_event_stream();
    stream.push(json!({"type": "error", "reason": "error", "error": message}));
    stream
}

/// The chat implementations of a provider: the single one, or the list keyed by `model.api` whose later entry of an API replaces an earlier one.
struct StreamsByApi {
    single: Option<ProviderStreams>,
    by_api: Vec<(String, ProviderStreams)>,
}

impl StreamsByApi {
    fn find(&self, model: &Value) -> Option<&ProviderStreams> {
        if self.single.is_some() {
            return self.single.as_ref();
        }
        let api = model["api"].as_str()?;
        self.by_api
            .iter()
            .rev()
            .find(|(name, _)| name == api)
            .map(|(_, entry)| entry)
    }
}

/// `createProvider(options)`.
pub fn create_provider(options: CreateProviderOptions) -> Result<Provider, String> {
    let id = options.id.clone();
    let (single, by_api): (Option<ProviderStreams>, Vec<(String, ProviderStreams)>) =
        match options.api {
            Some(ProviderApi::Single(streams)) => (Some(streams), Vec::new()),
            Some(ProviderApi::ByApi(list)) => (None, list),
            None => (None, Vec::new()),
        };
    let streams: Vec<&ProviderStreams> = match &single {
        Some(single) => vec![single],
        None => by_api.iter().map(|(_, entry)| entry).collect(),
    };
    if streams.is_empty() && options.images.is_empty() && options.classifiers.is_empty() {
        return Err(format!(
            r#"Provider {id}: at least one of "api", "images", or "classifiers" is required."#
        ));
    }
    let any_fetch_deferred = streams.iter().any(|entry| entry.fetch_deferred.is_some());
    let any_cancel_deferred = streams.iter().any(|entry| entry.cancel_deferred.is_some());
    drop(streams);
    struct CatalogSnapshot {
        all: Vec<ProviderModel>,
        chat: Vec<ProviderModel>,
    }
    let build_catalog = |baseline: &[Value], dynamic: &[Value]| -> CatalogSnapshot {
        let mut merged: Vec<Value> = Vec::with_capacity(baseline.len() + dynamic.len());
        merged.extend(baseline.iter().cloned());
        if !dynamic.is_empty() {
            // Merged once per publish, not per read (upstream: packages/ai/src/models.ts, Pi 073b0bae8 / #12311).
            let mut index_by_key: HashMap<(String, String), usize> =
                HashMap::with_capacity(merged.len() + dynamic.len());
            let key_of = |model: &Value| -> (String, String) {
                (
                    model_type(model).to_string(),
                    serde_json::to_string(&model["id"]).unwrap_or_default(),
                )
            };
            for (index, entry) in merged.iter().enumerate() {
                index_by_key.entry(key_of(entry)).or_insert(index);
            }
            for model in dynamic {
                let key = key_of(model);
                if let Some(&index) = index_by_key.get(&key) {
                    merged[index] = model.clone();
                } else {
                    index_by_key.insert(key, merged.len());
                    merged.push(model.clone());
                }
            }
        }
        let all: Vec<ProviderModel> = merged.into_iter().map(Arc::new).collect();
        let chat: Vec<ProviderModel> = all
            .iter()
            .filter(|model| model_type(model) == "chat")
            .cloned()
            .collect();
        CatalogSnapshot { all, chat }
    };
    let baseline = Arc::new(options.models);
    let catalog: Arc<RwLock<CatalogSnapshot>> =
        Arc::new(RwLock::new(build_catalog(&baseline, &[])));
    let streams_of = Arc::new(StreamsByApi { single, by_api });
    let get_models = {
        let catalog = catalog.clone();
        Arc::new(move || Ok(catalog.read().unwrap().chat.clone()))
    };
    let get_all_models = {
        let catalog = catalog.clone();
        Arc::new(move || Ok(catalog.read().unwrap().all.clone()))
            as Arc<dyn Fn() -> ProviderResult<Vec<ProviderModel>> + Send + Sync>
    };
    let refresh_models = options.fetch_models.map(|fetch| {
        let catalog = catalog.clone();
        let baseline = baseline.clone();
        let id = id.clone();
        Arc::new(move |context: RefreshModelsContext| -> ProviderResult<()> {
            let publish = |publication: ModelsPublication| (context.publish)(publication);
            let setter =
                |value: Vec<Value>| -> Option<Arc<dyn Fn() -> ProviderResult<()> + Send + Sync>> {
                    let catalog = catalog.clone();
                    let baseline = baseline.clone();
                    Some(Arc::new(move || {
                        let next = build_catalog(&baseline, &value);
                        *catalog.write().unwrap() = next;
                        Ok(())
                    }))
                };
            if let Some(stored) = &context.stored {
                let restored: Vec<Value> = stored["models"]
                    .as_array()
                    .map(Vec::as_slice)
                    .unwrap_or_default()
                    .iter()
                    .filter(|model| model["provider"] == id.as_str())
                    .cloned()
                    .collect();
                if !publish(ModelsPublication {
                    persist: None,
                    update: setter(restored),
                })? {
                    return Ok(());
                }
            }
            if !context.allow_network || context.signal.is_cancelled() {
                return Ok(());
            }
            let fetched = fetch(&context)?;
            if context.signal.is_cancelled() {
                return Ok(());
            }
            let refreshed: Vec<Value> = fetched
                .into_iter()
                .filter(|model| matches!(model_type(model), "chat" | "image" | "classifier"))
                .collect();
            publish(ModelsPublication {
                persist: Some(json!({"models": refreshed, "checkedAt": now_ms()})),
                update: setter(refreshed),
            })?;
            Ok(())
        }) as Arc<dyn Fn(RefreshModelsContext) -> ProviderResult<()> + Send + Sync>
    });
    let dispatch = |simple: bool| -> ProviderStreamFn {
        let streams_of = streams_of.clone();
        let id = id.clone();
        Arc::new(
            move |model: ProviderModel, context: Value, options: ProviderStreamOptions| {
                match streams_of.find(&model) {
                    None => Ok(setup_error_stream(
                        &model,
                        &format!(
                            r#"Provider {id} has no API implementation for "{}""#,
                            api_text(&model)
                        ),
                    )),
                    Some(implementation) => (if simple {
                        &implementation.stream_simple
                    } else {
                        &implementation.stream
                    })(model, context, options),
                }
            },
        )
    };
    let mut provider = Provider {
        id: id.clone(),
        name: options.name.unwrap_or_else(|| id.clone()),
        base_url: options.base_url,
        headers: options.headers,
        refresh_models_auth: None,
        auth: options.auth,
        get_models,
        filter_models: options.filter_models,
        get_all_models: Some(get_all_models),
        filter_all_models: options.filter_all_models,
        refresh_models,
        stream: dispatch(false),
        stream_simple: dispatch(true),
        fetch_deferred: None,
        cancel_deferred: None,
        generate_images: None,
        classify: None,
    };
    if any_fetch_deferred {
        let streams_of = streams_of.clone();
        let id = id.clone();
        provider.fetch_deferred = Some(Arc::new(
            move |model: ProviderModel, handle: Value, options: ProviderStreamOptions| {
                match streams_of
                    .find(&model)
                    .and_then(|implementation| implementation.fetch_deferred.as_ref())
                {
                    None => Ok(setup_error_stream(
                        &model,
                        &format!(
                            r#"Provider {id} does not support deferred responses for "{}""#,
                            api_text(&model)
                        ),
                    )),
                    Some(fetch) => Ok(fetch(model.clone(), handle, options)
                        .unwrap_or_else(|error| setup_error_stream(&model, &error))),
                }
            },
        ));
    }
    if any_cancel_deferred {
        let streams_of = streams_of.clone();
        let id = id.clone();
        provider.cancel_deferred = Some(Arc::new(
            move |model: ProviderModel, handle: Value, options: ProviderStreamOptions| {
                match streams_of
                    .find(&model)
                    .and_then(|implementation| implementation.cancel_deferred.as_ref())
                {
                    None => Err(format!(
                        r#"Provider {id} cannot cancel deferred responses for "{}""#,
                        api_text(&model)
                    )),
                    Some(cancel) => cancel(model, handle, options),
                }
            },
        ));
    }
    if !options.images.is_empty() {
        let images = options.images;
        let id = id.clone();
        provider.generate_images = Some(Arc::new(
            move |model: ProviderModel, request: Value, options: ProviderOperationOptions| {
                match images
                    .iter()
                    .rev()
                    .find(|(api, _)| model["api"].as_str() == Some(api.as_str()))
                {
                    None => Ok(json!({
                        "api": model["api"], "provider": model["provider"], "model": model["id"], "output": [], "stopReason": "error",
                        "errorMessage": format!(r#"Provider {id} has no image generation implementation for "{}""#, api_text(&model)), "timestamp": now_ms(),
                    })),
                    Some((_, implementation)) => implementation(model.clone(), request, options),
                }
            },
        ));
    }
    if !options.classifiers.is_empty() {
        let classifiers = options.classifiers;
        let id = id.clone();
        provider.classify = Some(Arc::new(
            move |model: ProviderModel, request: Value, options: ProviderOperationOptions| {
                match classifiers
                    .iter()
                    .rev()
                    .find(|(api, _)| model["api"].as_str() == Some(api.as_str()))
                {
                    None => Ok(json!({
                        "api": model["api"], "provider": model["provider"], "model": model["id"], "answers": {}, "stopReason": "error",
                        "errorMessage": format!(r#"Provider {id} has no classifier implementation for "{}""#, api_text(&model)), "timestamp": now_ms(),
                    })),
                    Some((_, implementation)) => implementation(model.clone(), request, options),
                }
            },
        ));
    }
    Ok(provider)
}
