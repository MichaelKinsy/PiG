use pig_sdk::*;
use serde_json::{Value, json};
use std::sync::{Arc, Mutex};

pub fn new_extension() -> Extension {
    let mut extension = Extension::new("provider-object-rust");
    if std::env::var("CARRIER_ROLE").unwrap_or_default() != "reader" {
        extension.register_native_provider(make_provider()).unwrap()
    }
    extension.command(
        "remote-carrier-probe",
        "Exercise a foreign Provider",
        |ctx, path| match probe(ctx, path) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err),
        },
    );
    extension
}
fn make_provider() -> Arc<Provider> {
    let model = Arc::new(
        json!({"id":"carrier-model","name":"Carrier","provider":"carrier-provider","api":"openai-completions","baseUrl":"http://127.0.0.1:9","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4000,"maxTokens":100}),
    );
    let models = Arc::new(Mutex::new(vec![model.clone()]));
    let stream: ProviderStreamFn = Arc::new(|model, _, options| {
        let meta = &options.values["metadata"];
        if meta["fail"] == true {
            return Err("carrier stream failed".into());
        }
        let stream = Arc::new(ModelEventStream::new());
        let mut message = json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[],"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1});
        if meta["wait"] == true {
            let stream = stream.clone();
            message["stopReason"] = json!("aborted");
            message["errorMessage"] = json!("carrier cancelled");
            options.signal.on_cancel(Arc::new(move || {
                stream.push(json!({"type":"error","reason":"aborted","error":message}))
            }));
        } else {
            message["content"] =
                json!([{"type":"text","text":meta["method"].as_str().unwrap_or("carrier answer")}]);
            stream.push(json!({"type":"done","reason":"stop","message":message}));
        }
        Ok(stream)
    });
    Arc::new(Provider {
        id: "carrier-provider".into(),
        name: "Carrier Provider".into(),
        base_url: Some("http://127.0.0.1:9".into()),
        headers: Some(json!({"X-Carrier":"present"})),
        auth: ProviderAuth {
            api_key: Some(APIKeyAuth {
                name: "Carrier API key".into(),
                check: Some(Arc::new(|input| {
                    Ok(Some(AuthCheck {
                        kind: "api_key".into(),
                        source: (input.ctx.env)("CARRIER_SOURCE".into())?,
                    }))
                })),
                resolve: Arc::new(|input| {
                    let mut auth = json!({});
                    let key = input
                        .credential
                        .as_ref()
                        .and_then(|v| v["key"].as_str())
                        .map(str::to_owned)
                        .or((input.ctx.env)("CARRIER_KEY".into())?);
                    if let Some(key) = key {
                        auth["apiKey"] = json!(key)
                    }
                    Ok(Some(AuthResult {
                        auth,
                        env: None,
                        source: Some("caller context".into()),
                    }))
                }),
                login: Some(Arc::new(|input| {
                    Ok(
                        json!({"type":"api_key","key":(input.prompt)(json!({"type":"secret","message":"Carrier key"}))?}),
                    )
                })),
            }),
            oauth: Some(OAuthAuth {
                name: "Carrier OAuth".into(),
                is_subscription: Some(true),
                login_label: Some("Carrier login".into()),
                login: Arc::new(|input| {
                    Ok(
                        json!({"type":"oauth","access":(input.prompt)(json!({"type":"text","message":"Carrier OAuth"}))?,"refresh":"refresh","expires":100}),
                    )
                }),
                refresh: Arc::new(|mut credential, _| {
                    credential["access"] = json!("rotated");
                    credential["expires"] = json!(200);
                    Ok(credential)
                }),
                to_auth: Arc::new(|credential| {
                    Ok(json!({"apiKey":credential["access"],"baseUrl":"https://carrier.invalid"}))
                }),
            }),
        },
        get_models: {
            let models = models.clone();
            Arc::new(move || Ok(models.lock().unwrap().clone()))
        },
        filter_models: Some(Arc::new(|models, credential| {
            Ok(
                if credential.as_ref().is_some_and(|c| c["key"] == "selected") {
                    models[..1].to_vec()
                } else {
                    vec![]
                },
            )
        })),
        refresh_models: Some(Arc::new(move |input| {
            if input.force != Some(true) {
                return Ok(());
            }
            let models = models.clone();
            let model = model.clone();
            (input.publish)(ModelsPublication {
                persist: Some(Value::Null),
                update: Some(Arc::new(move || {
                    let mut value = model.as_ref().clone();
                    value["id"] = json!("refreshed");
                    models.lock().unwrap().push(Arc::new(value));
                    Ok(())
                })),
            })?;
            Ok(())
        })),
        stream: stream.clone(),
        stream_simple: {
            let stream = stream.clone();
            Arc::new(move |model, context, mut options| {
                options.values = json!({"metadata":{"method":"simple"}});
                stream(model, context, options)
            })
        },
        fetch_deferred: Some({
            let stream = stream.clone();
            Arc::new(move |model, handle, mut options| {
                options.values = json!({"metadata":{"method":handle["id"]}});
                stream(model, json!({}), options)
            })
        }),
        cancel_deferred: Some(Arc::new(|_, handle, _| {
            if handle["id"] == "deferred" {
                Ok(())
            } else {
                Err("wrong deferred handle".into())
            }
        })),
        generate_images: None,
        classify: None,
    })
}
fn probe(ctx: &Context, path: &str) -> Result<(), String> {
    let provider = ctx
        .model_registry()
        .get_registered_native_provider("carrier-provider")
        .map_err(|e| e.to_string())?
        .ok_or("foreign Provider absent")?;
    let again = ctx
        .model_registry()
        .get_provider("carrier-provider")
        .map_err(|e| e.to_string())?
        .ok_or("Provider absent")?;
    assert!(Arc::ptr_eq(&provider, &again));
    let models = (provider.get_models)()?;
    let model = models[0].clone();
    let filtered = provider.filter_models.as_ref().unwrap()(
        &models,
        Some(json!({"type":"api_key","key":"selected"})),
    )?;
    assert!(Arc::ptr_eq(&filtered[0], &model));
    assert!(provider.filter_models.as_ref().unwrap()(&models, None)?.is_empty());
    let input = APIKeyAuthInput {
        ctx: AuthContext {
            env: Arc::new(|name| Ok(Some(format!("injected:{name}")))),
            file_exists: Arc::new(|_| Ok(false)),
        },
        credential: None,
        signal: ProviderSignal::new(),
    };
    let api = provider.auth.api_key.as_ref().unwrap();
    let oauth = provider.auth.oauth.as_ref().unwrap();
    let check = api.check.as_ref().unwrap()(input.clone())?;
    let auth = (api.resolve)(input)?;
    let interaction = AuthInteraction {
        signal: ProviderSignal::new(),
        prompt: Arc::new(|prompt| Ok(prompt["message"].as_str().unwrap().into())),
        notify: Arc::new(|_| Ok(())),
    };
    let api_login = api.login.as_ref().unwrap()(interaction.clone())?;
    let login = (oauth.login)(interaction)?;
    let rotated = (oauth.refresh)(login.clone(), ProviderSignal::new())?;
    let oauth_auth = (oauth.to_auth)(rotated.clone())?;
    let order = Arc::new(Mutex::new(vec![]));
    let recorded = order.clone();
    provider.refresh_models.as_ref().unwrap()(RefreshModelsContext {
        credential: None,
        stored: None,
        allow_network: false,
        force: Some(true),
        signal: ProviderSignal::new(),
        publish: Arc::new(move |publication| {
            assert_eq!(publication.persist, Some(Value::Null));
            recorded.lock().unwrap().push("persist");
            publication.update.as_ref().unwrap()()?;
            recorded.lock().unwrap().push("update");
            Ok(true)
        }),
    })?;
    let result = (provider.stream)(
        model.clone(),
        json!({"messages":[]}),
        ProviderStreamOptions::default(),
    )?
    .result()
    .unwrap();
    let simple = (provider.stream_simple)(
        model.clone(),
        json!({"messages":[]}),
        ProviderStreamOptions::default(),
    )?
    .result()
    .unwrap();
    let deferred = provider.fetch_deferred.as_ref().unwrap()(
        model.clone(),
        json!({"id":"deferred"}),
        ProviderStreamOptions::default(),
    )?
    .result()
    .unwrap();
    provider.cancel_deferred.as_ref().unwrap()(
        model.clone(),
        json!({"id":"deferred"}),
        ProviderStreamOptions::default(),
    )?;
    let signal = ProviderSignal::new();
    let waiting = (provider.stream)(
        model,
        json!({"messages":[]}),
        ProviderStreamOptions {
            signal: signal.clone(),
            values: json!({"metadata":{"wait":true}}),
            ..Default::default()
        },
    )?;
    signal.cancel();
    let cancelled = waiting.result().unwrap();
    let ids = (provider.get_models)()?
        .iter()
        .map(|model| model["id"].clone())
        .collect::<Vec<_>>();
    let output = json!({"headers":provider.headers,"check":check,"auth":auth,"apiLogin":api_login,"login":login,"rotated":rotated,"oauth":oauth_auth,"order":*order.lock().unwrap(),"models":ids,"result":result["content"],"simple":simple["content"],"deferred":deferred["content"],"cancelled":cancelled["stopReason"]});
    std::fs::write(
        path,
        serde_json::to_vec(&output).map_err(|e| e.to_string())?,
    )
    .map_err(|e| e.to_string())
}
