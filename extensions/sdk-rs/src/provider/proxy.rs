use super::*;

struct Lease {
    conn: Arc<Connection>,
    handle: String,
    token: String,
}
impl Drop for Lease {
    fn drop(&mut self) {
        let _ = self.conn.write_envelope(&Envelope {
            msg_type: "call".into(),
            call: Some(CallMsg {
                method: "provider.release".into(),
                args: Some(json!({"handle":self.handle,"token":self.token})),
                parent_request_id: None,
            }),
            ..Default::default()
        });
    }
}
#[derive(Clone)]
struct Proxy {
    conn: Arc<Connection>,
    state: Arc<ProviderObjects>,
    decl: Value,
    _lease: Arc<Lease>,
}
struct Scope {
    proxy: Proxy,
    id: String,
    subscription: Option<ProviderSignalSubscription>,
    finished: bool,
}
impl Drop for Scope {
    fn drop(&mut self) {
        self.subscription.take();
        self.proxy.state.callbacks.lock().unwrap().remove(&self.id);
        if !self.finished {
            self.proxy.cancel(&self.id)
        }
    }
}
struct Operation {
    pending: crate::protocol::PendingCall,
    scope: Scope,
}
impl Operation {
    fn wait(mut self) -> ProviderResult<Value> {
        let result = self
            .scope
            .proxy
            .conn
            .wait_call(self.pending)
            .map_err(|e| e.to_string())
            .and_then(result_value);
        self.scope.finished = true;
        result
    }
}
impl Proxy {
    fn cancel(&self, id: &str) {
        let _ = self.conn.write_envelope(&Envelope {
            msg_type: "call".into(),
            call: Some(CallMsg {
                method: "cancelModelStream".into(),
                args: Some(json!({"streamId":id})),
                parent_request_id: None,
            }),
            ..Default::default()
        });
    }
    fn begin(
        &self,
        method: &str,
        params: Value,
        callbacks: Callbacks,
        signal: ProviderSignal,
        stream_id: Option<String>,
    ) -> ProviderResult<Operation> {
        let id = stream_id.clone().unwrap_or_else(|| self.state.key());
        self.state
            .callbacks
            .lock()
            .unwrap()
            .insert(id.clone(), callbacks);
        let mut scope = Scope {
            proxy: self.clone(),
            id: id.clone(),
            subscription: None,
            finished: false,
        };
        let pending=self.conn.begin_call_for(None,"provider.object",Some(json!({"handle":self.decl["handle"],"method":method,"params":params,"callbackId":id,"streamId":stream_id.unwrap_or_default()}))).map_err(|e|e.to_string())?;
        let proxy = self.clone();
        scope.subscription = Some(signal.subscribe(Arc::new(move || proxy.cancel(&id))));
        Ok(Operation { pending, scope })
    }
    fn invoke(
        &self,
        method: &str,
        params: Value,
        callbacks: Callbacks,
        signal: ProviderSignal,
    ) -> ProviderResult<Value> {
        self.begin(method, params, callbacks, signal, None)?.wait()
    }
    fn plain(&self, method: &str, params: Value) -> ProviderResult<Value> {
        self.invoke(method, params, HashMap::new(), ProviderSignal::new())
    }
    fn stream(
        &self,
        method: &str,
        model: ProviderModel,
        context: Value,
        options: ProviderStreamOptions,
    ) -> ProviderResult<Arc<ModelEventStream>> {
        let stream = Arc::new(ModelEventStream::new());
        let id = self.state.key();
        self.state
            .streams
            .lock()
            .unwrap()
            .insert(id.clone(), stream.clone());
        let mut callbacks: Callbacks = HashMap::new();
        let mut names = vec![];
        if let Some(callback) = options.on_payload {
            names.push("onPayload");
            let model = model.clone();
            callbacks.insert(
                "onPayload".into(),
                Arc::new(move |args| callback(args["value"].clone(), model.clone())),
            );
        }
        if let Some(callback) = options.on_response {
            names.push("onResponse");
            let model = model.clone();
            callbacks.insert(
                "onResponse".into(),
                Arc::new(move |args| {
                    callback(args["value"].clone(), model.clone())?;
                    Ok(Value::Null)
                }),
            );
        }
        if let Some(callback) = options.transform_headers {
            names.push("transformHeaders");
            callbacks.insert(
                "transformHeaders".into(),
                Arc::new(move |args| callback(args["value"].clone())),
            );
        }
        let mut params = json!({"model":model.as_ref(),"options":options.values,"callbacks":names,"aborted":options.signal.is_cancelled()});
        params[if method == "fetchDeferred" {
            "handle"
        } else {
            "context"
        }] = context;
        let operation =
            match self.begin(method, params, callbacks, options.signal, Some(id.clone())) {
                Ok(op) => op,
                Err(err) => {
                    self.state.streams.lock().unwrap().remove(&id);
                    return Err(err);
                }
            };
        let running = stream.clone();
        let state = self.state.clone();
        state.workers.start();
        let workers = state.workers.clone();
        if let Err(err) = std::thread::Builder::new()
            .name("pig-provider-stream".into())
            .spawn(move || {
                let error =
                    std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| operation.wait()))
                        .unwrap_or_else(|_| Err("Provider stream worker panicked".into()))
                        .err()
                        .unwrap_or_else(|| "Provider stream ended without a terminal event".into());
                running.mark_started(Some(error.clone()));
                running.push(crate::context::model_stream_error_event(&error, &model));
                state.streams.lock().unwrap().remove(&id);
                workers.finish();
            })
        {
            self.state.workers.finish();
            return Err(err.to_string());
        }
        stream.wait_started()?;
        Ok(stream)
    }
}
fn auth_callbacks(input: &APIKeyAuthInput) -> Callbacks {
    let mut result: Callbacks = HashMap::new();
    let env = input.ctx.env.clone();
    let exists = input.ctx.file_exists.clone();
    result.insert(
        "env".into(),
        Arc::new(move |args| {
            Ok(json!(env(args["name"]
                .as_str()
                .ok_or("missing env name")?
                .into())?))
        }),
    );
    result.insert(
        "fileExists".into(),
        Arc::new(move |args| {
            Ok(json!(exists(
                args["path"].as_str().ok_or("missing path")?.into()
            )?))
        }),
    );
    result
}
fn interaction_callbacks(input: &AuthInteraction) -> Callbacks {
    let mut result: Callbacks = HashMap::new();
    let prompt = input.prompt.clone();
    let notify = input.notify.clone();
    result.insert(
        "prompt".into(),
        Arc::new(move |args| Ok(json!(prompt(args["prompt"].clone())?))),
    );
    result.insert(
        "notify".into(),
        Arc::new(move |args| {
            notify(args["event"].clone())?;
            Ok(Value::Null)
        }),
    );
    result
}
impl ProviderObjects {
    pub(crate) fn get(
        self: &Arc<Self>,
        conn: Arc<Connection>,
        decl: Value,
    ) -> ProviderResult<Arc<Provider>> {
        let key = decl["key"]
            .as_str()
            .ok_or("missing Provider callback key")?;
        if let Some(local) = self.native.lock().unwrap().get(key) {
            return Ok(local.clone());
        }
        let handle = decl["handle"]
            .as_str()
            .ok_or("missing Provider object handle")?
            .to_owned();
        let id = decl["id"].as_str().ok_or("missing Provider id")?.to_owned();
        let mut proxies = self.proxies.lock().unwrap();
        if let Some((current, weak)) = proxies.get(&id) {
            if current == &handle {
                if let Some(provider) = weak.upgrade() {
                    return Ok(provider);
                }
            }
        }
        let lease = Arc::new(Lease {
            conn: conn.clone(),
            handle: handle.clone(),
            token: self.key(),
        });
        call_value(
            &conn,
            None,
            "provider.retain",
            json!({"handle":lease.handle,"token":lease.token}),
        )?;
        let proxy = Proxy {
            conn,
            state: self.clone(),
            decl: decl.clone(),
            _lease: lease,
        };
        let has = |method: &str| {
            decl["methods"]
                .as_array()
                .is_some_and(|methods| methods.contains(&json!(method)))
        };
        let operation = |method: &'static str| -> Option<ProviderOperationFn> {
            has(method).then(|| {
                let proxy = proxy.clone();
                Arc::new(move |model: ProviderModel, context: Value, options: ProviderOperationOptions| {
                    proxy.invoke(method, json!({"model":model.as_ref(),"context":context,"options":options.values}), HashMap::new(), options.signal)
                }) as ProviderOperationFn
            })
        };
        let stream = |method: &str| -> ProviderStreamFn {
            let proxy = proxy.clone();
            let method = method.to_owned();
            Arc::new(move |model, context, options| proxy.stream(&method, model, context, options))
        };
        let api_key = if decl["auth"]["apiKey"].is_object() {
            Some(APIKeyAuth {
                name: decl["auth"]["apiKey"]["name"]
                    .as_str()
                    .unwrap_or_default()
                    .into(),
                resolve: {
                    let proxy = proxy.clone();
                    Arc::new(move |input| {
                        serde_json::from_value(proxy.invoke(
                            "auth.apiKey.resolve",
                            json!({"credential":input.credential}),
                            auth_callbacks(&input),
                            input.signal,
                        )?)
                        .map_err(|e| e.to_string())
                    })
                },
                check: if has("auth.apiKey.check") {
                    let proxy = proxy.clone();
                    Some(Arc::new(move |input: APIKeyAuthInput| {
                        serde_json::from_value(proxy.invoke(
                            "auth.apiKey.check",
                            json!({"credential":input.credential}),
                            auth_callbacks(&input),
                            input.signal,
                        )?)
                        .map_err(|e| e.to_string())
                    }))
                } else {
                    None
                },
                login: if has("auth.apiKey.login") {
                    let proxy = proxy.clone();
                    Some(Arc::new(move |input: AuthInteraction| {
                        proxy.invoke(
                            "auth.apiKey.login",
                            json!({}),
                            interaction_callbacks(&input),
                            input.signal,
                        )
                    }))
                } else {
                    None
                },
            })
        } else {
            None
        };
        let oauth = if decl["auth"]["oauth"].is_object() {
            Some(OAuthAuth {
                name: decl["auth"]["oauth"]["name"]
                    .as_str()
                    .unwrap_or_default()
                    .into(),
                is_subscription: decl["auth"]["oauth"]["isSubscription"].as_bool(),
                login_label: decl["auth"]["oauth"]["loginLabel"]
                    .as_str()
                    .map(str::to_owned),
                login: {
                    let proxy = proxy.clone();
                    Arc::new(move |input| {
                        proxy.invoke(
                            "auth.oauth.login",
                            json!({}),
                            interaction_callbacks(&input),
                            input.signal,
                        )
                    })
                },
                refresh: {
                    let proxy = proxy.clone();
                    Arc::new(move |credential, signal| {
                        proxy.invoke(
                            "auth.oauth.refresh",
                            json!({"credential":credential}),
                            HashMap::new(),
                            signal,
                        )
                    })
                },
                to_auth: {
                    let proxy = proxy.clone();
                    Arc::new(move |credential| {
                        proxy.plain("auth.oauth.toAuth", json!({"credential":credential}))
                    })
                },
            })
        } else {
            None
        };
        let provider = Arc::new(Provider {
            id: decl["id"].as_str().ok_or("missing Provider id")?.into(),
            name: decl["name"].as_str().ok_or("missing Provider name")?.into(),
            base_url: decl["baseUrl"].as_str().map(str::to_owned),
            headers: optional(&decl["headers"]),
            auth: ProviderAuth { api_key, oauth },
            get_models: {
                let proxy = proxy.clone();
                Arc::new(move || {
                    Ok(proxy
                        .plain("getModels", json!({}))?
                        .as_array()
                        .ok_or("getModels must return an array")?
                        .iter()
                        .cloned()
                        .map(Arc::new)
                        .collect())
                })
            },
            filter_models: if has("filterModels") {
                let proxy = proxy.clone();
                Some(Arc::new(move |models: &[ProviderModel], credential| {
                    let result=proxy.plain("filterModels",json!({"models":models.iter().map(|m|m.as_ref()).collect::<Vec<_>>(),"credential":credential}))?;
                    let mut filtered = Vec::new();
                    for (i, model) in result["models"]
                        .as_array()
                        .ok_or("filterModels result must be an array")?
                        .iter()
                        .enumerate()
                    {
                        if let Some(index) = result["indices"][i].as_u64() {
                            filtered.push(
                                models
                                    .get(index as usize)
                                    .ok_or("filterModels index out of range")?
                                    .clone(),
                            )
                        } else {
                            filtered.push(Arc::new(model.clone()))
                        }
                    }
                    Ok(filtered)
                }))
            } else {
                None
            },
            refresh_models: if has("refreshModels") {
                let proxy = proxy.clone();
                Some(Arc::new(move |input: RefreshModelsContext| {
                    let mut callbacks: Callbacks = HashMap::new();
                    let nested = proxy.clone();
                    let publish = input.publish.clone();
                    callbacks.insert(
                        "publish".into(),
                        Arc::new(move |args| {
                            let update = args["token"].as_str().map(|token| {
                                let nested = nested.clone();
                                let token = token.to_owned();
                                Arc::new(move || {
                                    nested.plain("update", json!({"token":token}))?;
                                    Ok(())
                                })
                                    as Arc<dyn Fn() -> ProviderResult<()> + Send + Sync>
                            });
                            Ok(json!(publish(ModelsPublication {
                                persist: args["publication"].get("persist").cloned(),
                                update
                            })?))
                        }),
                    );
                    proxy.invoke("refreshModels",json!({"credential":input.credential,"stored":input.stored,"allowNetwork":input.allow_network,"force":input.force}),callbacks,input.signal)?;
                    Ok(())
                }))
            } else {
                None
            },
            stream: stream("stream"),
            stream_simple: stream("streamSimple"),
            fetch_deferred: has("fetchDeferred").then(|| stream("fetchDeferred")),
            cancel_deferred: if has("cancelDeferred") {
                let proxy = proxy.clone();
                Some(Arc::new(
                    move |model: ProviderModel, handle, options: ProviderStreamOptions| {
                        proxy.invoke("cancelDeferred",json!({"model":model.as_ref(),"handle":handle,"options":options.values}),HashMap::new(),options.signal)?;
                        Ok(())
                    },
                ))
            } else {
                None
            },
            generate_images: operation("generateImages"),
            classify: operation("classify"),
        });
        proxies.insert(id, (handle, Arc::downgrade(&provider)));
        Ok(provider)
    }
}
