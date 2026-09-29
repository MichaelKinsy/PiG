use std::collections::{HashMap, HashSet};
use std::io;
use std::sync::{Arc, Mutex};
use std::sync::atomic::{AtomicU64, Ordering};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use crate::{AutocompleteItem, Context};

/// Complete suggestions; cursor columns in this API count UTF-16 code units.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct AutocompleteSuggestions {
    pub items: Vec<AutocompleteItem>,
    pub prefix: String,
}

/// Provider-owned buffer replacement and cursor.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AutocompleteCompletion {
    pub lines: Vec<String>,
    pub cursor_line: usize,
    pub cursor_col: usize,
}

pub type AutocompleteSuggestionsFn = Arc<dyn Fn(&Context, &[String], usize, usize, bool) -> io::Result<Option<AutocompleteSuggestions>> + Send + Sync>;
pub type AutocompleteApplyFn = Arc<dyn Fn(&Context, &[String], usize, usize, &AutocompleteItem, &str) -> io::Result<AutocompleteCompletion> + Send + Sync>;
pub type AutocompleteFileTriggerFn = Arc<dyn Fn(&Context, &[String], usize, usize) -> io::Result<bool> + Send + Sync>;

/// A retained provider instance. Every invocation receives its current cancellation/host context.
pub struct AutocompleteProvider {
    pub trigger_characters: Vec<String>,
    pub get_suggestions: AutocompleteSuggestionsFn,
    pub apply_completion: AutocompleteApplyFn,
    pub should_trigger_file_completion: Option<AutocompleteFileTriggerFn>,
}

pub type AutocompleteProviderFactory = Arc<dyn Fn(&Context, Arc<AutocompleteProvider>) -> io::Result<Arc<AutocompleteProvider>> + Send + Sync>;

#[derive(Default)]
pub(crate) struct AutocompleteRegistry {
    next: AtomicU64,
    factories: Mutex<HashMap<String, AutocompleteProviderFactory>>,
    invoked: Mutex<HashSet<String>>,
    providers: Mutex<HashMap<String, Arc<AutocompleteProvider>>>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Descriptor {
    id: String,
    #[serde(default)]
    trigger_characters: Option<Vec<String>>,
    has_file_trigger: bool,
}

fn invoke<T: serde::de::DeserializeOwned>(ctx: &Context, args: Value) -> io::Result<T> {
    if ctx.is_cancelled() { return Err(io::Error::new(io::ErrorKind::Interrupted, "autocomplete cancelled")); }
    let value = ctx.call_host("ui.autocomplete.invoke", Some(args))?.unwrap_or(Value::Null);
    serde_json::from_value(value).map_err(io::Error::other)
}

struct CurrentLease { connection: Arc<crate::protocol::Connection>, id: String }
impl Drop for CurrentLease {
    fn drop(&mut self) { let _ = self.connection.notify("ui.autocomplete.release", Some(json!({"id":self.id}))); }
}

fn proxy(ctx: &Context, desc: Descriptor) -> Arc<AutocompleteProvider> {
    let lease = Arc::new(CurrentLease { connection: ctx.conn.clone(), id: desc.id });
    let get_id = lease.clone();
    let apply_id = lease.clone();
    Arc::new(AutocompleteProvider {
        trigger_characters: desc.trigger_characters.unwrap_or_default(),
        get_suggestions: Arc::new(move |ctx, lines, line, col, force| invoke(ctx, json!({"id":get_id.id,"operation":"getSuggestions","lines":lines,"cursorLine":line,"cursorCol":col,"force":force}))),
        apply_completion: Arc::new(move |ctx, lines, line, col, item, prefix| invoke(ctx, json!({"id":apply_id.id,"operation":"applyCompletion","lines":lines,"cursorLine":line,"cursorCol":col,"item":item,"prefix":prefix}))),
        should_trigger_file_completion: if desc.has_file_trigger { Some(Arc::new(move |ctx, lines, line, col| invoke(ctx, json!({"id":lease.id,"operation":"shouldTriggerFileCompletion","lines":lines,"cursorLine":line,"cursorCol":col})))) } else { None },
    })
}

impl Context {
    /// Append a factory and wait for the rebuilt chain. Without a UI, the factory is neither called nor retained.
    pub fn add_autocomplete_provider(&self, factory: AutocompleteProviderFactory) -> io::Result<()> {
        if !self.has_ui() { return Ok(()); }
        let registry = &self.conn.autocomplete;
        let id = format!("ac-factory-{}", registry.next.fetch_add(1, Ordering::Relaxed));
        registry.factories.lock().unwrap().insert(id.clone(), factory);
        let result = self.call_host("ui.addAutocompleteProvider", Some(json!({"factoryId":id})));
        if !registry.invoked.lock().unwrap().contains(&id) { registry.factories.lock().unwrap().remove(&id); }
        result?;
        Ok(())
    }
}

impl AutocompleteRegistry {
    pub(crate) fn release(&self, id: &str) { self.providers.lock().unwrap().remove(id); }

    pub(crate) fn clear(&self) {
        self.factories.lock().unwrap().clear();
        self.invoked.lock().unwrap().clear();
        self.providers.lock().unwrap().clear();
    }

    pub(crate) fn dispatch(&self, ctx: &Context, args: Value) -> io::Result<Value> {
        let operation = args["operation"].as_str().unwrap_or("");
        if operation == "wrap" {
            let factory = self.factories.lock().unwrap().get(args["factoryId"].as_str().unwrap_or("")).cloned().ok_or_else(|| io::Error::other("autocomplete factory is no longer available"))?;
            self.invoked.lock().unwrap().insert(args["factoryId"].as_str().unwrap_or("").to_owned());
            let current: Descriptor = serde_json::from_value(args["current"].clone()).map_err(io::Error::other)?;
            let provider = factory(ctx, proxy(ctx, current))?;
            let id = format!("ac-provider-{}", self.next.fetch_add(1, Ordering::Relaxed));
            let result = json!({"id":id,"triggerCharacters":provider.trigger_characters,"hasFileTrigger":provider.should_trigger_file_completion.is_some()});
            self.providers.lock().unwrap().insert(id, provider);
            return Ok(result);
        }
        let provider = self.providers.lock().unwrap().get(args["id"].as_str().unwrap_or("")).cloned().ok_or_else(|| io::Error::other("autocomplete provider is no longer available"))?;
        let lines: Vec<String> = serde_json::from_value(args["lines"].clone()).map_err(io::Error::other)?;
        let line = args["cursorLine"].as_u64().unwrap_or(0) as usize;
        let col = args["cursorCol"].as_u64().unwrap_or(0) as usize;
        match operation {
            "getSuggestions" => serde_json::to_value((provider.get_suggestions)(ctx, &lines, line, col, args["force"].as_bool().unwrap_or(false))?).map_err(io::Error::other),
            "applyCompletion" => {
                let item: AutocompleteItem = serde_json::from_value(args["item"].clone()).map_err(io::Error::other)?;
                serde_json::to_value((provider.apply_completion)(ctx, &lines, line, col, &item, args["prefix"].as_str().unwrap_or(""))?).map_err(io::Error::other)
            }
            "shouldTriggerFileCompletion" => Ok(json!(match &provider.should_trigger_file_completion { Some(trigger) => trigger(ctx, &lines, line, col)?, None => true })),
            _ => Err(io::Error::other(format!("unknown autocomplete operation: {operation}"))),
        }
    }
}
