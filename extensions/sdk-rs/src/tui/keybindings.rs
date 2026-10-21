//! pi-tui's `keybindings.ts`: the effective keys of every keybinding.
//!
//! [`KeybindingsManager`] resolves each keybinding id to the user's override or the definition's default keys, matches raw terminal
//! input against them and reports conflicts between user overrides. The SDK installs the host's effective table (every `tui.*` and
//! `app.*` definition with the user's `keybindings.json` overrides) as the global manager when the host publishes it, as Pi's startup
//! does.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use serde_json::Value;

use super::keys::matches_key;
use super::tui_keybindings::TUI_KEYBINDING_DEFINITIONS;

/// pi-tui's `KeybindingDefinition`: the default keys and the description of one keybinding.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct KeybindingDefinition {
    pub default_keys: Vec<String>,
    pub description: Option<String>,
}

/// pi-tui's `KeybindingConflict`: a key claimed by more than one keybinding.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct KeybindingConflict {
    pub key: String,
    pub keybindings: Vec<String>,
}

/// pi-tui's `TUI_KEYBINDINGS`: the `tui.*` keybinding definitions, in Pi's order.
pub fn tui_keybindings() -> Vec<(String, KeybindingDefinition)> {
    TUI_KEYBINDING_DEFINITIONS
        .iter()
        .map(|(id, keys, description)| {
            (id.to_string(), KeybindingDefinition { default_keys: keys.iter().map(|k| k.to_string()).collect(), description: description.map(str::to_string) })
        })
        .collect()
}

fn normalize_keys(keys: &[String]) -> Vec<String> {
    let mut result: Vec<String> = Vec::new();
    for key in keys {
        if !result.contains(key) {
            result.push(key.clone());
        }
    }
    result
}

/// pi-tui's `KeybindingsManager`.
#[derive(Debug, Clone)]
pub struct KeybindingsManager {
    definitions: Vec<(String, KeybindingDefinition)>,
    user_bindings: Vec<(String, Vec<String>)>,
    keys_by_id: HashMap<String, Vec<String>>,
    conflicts: Vec<KeybindingConflict>,
}

impl KeybindingsManager {
    /// `new KeybindingsManager(definitions, userBindings)`; both tables keep their order, which the conflict report follows.
    pub fn new(definitions: Vec<(String, KeybindingDefinition)>, user_bindings: Vec<(String, Vec<String>)>) -> Self {
        let mut manager = KeybindingsManager { definitions, user_bindings, keys_by_id: HashMap::new(), conflicts: Vec::new() };
        manager.rebuild();
        manager
    }

    /// The manager of the host's published table: `{"definitions": {...}, "userBindings": {...}}`.
    pub fn from_wire(table: &Value) -> Self {
        let keys = |v: &Value| -> Vec<String> {
            match v {
                Value::String(s) => vec![s.clone()],
                Value::Array(list) => list.iter().filter_map(|k| k.as_str().map(str::to_string)).collect(),
                _ => Vec::new(),
            }
        };
        let definitions = table["definitions"]
            .as_object()
            .map(|o| o.iter().map(|(id, d)| (id.clone(), KeybindingDefinition { default_keys: keys(&d["defaultKeys"]), description: d["description"].as_str().map(str::to_string) })).collect())
            .unwrap_or_default();
        let user_bindings = table["userBindings"].as_object().map(|o| o.iter().map(|(id, k)| (id.clone(), keys(k))).collect()).unwrap_or_default();
        Self::new(definitions, user_bindings)
    }

    fn user_keys(&self, id: &str) -> Option<&Vec<String>> {
        self.user_bindings.iter().find(|(k, _)| k == id).map(|(_, v)| v)
    }

    fn rebuild(&mut self) {
        self.keys_by_id.clear();
        self.conflicts.clear();
        let mut claims: Vec<(String, Vec<String>)> = Vec::new();
        for (keybinding, keys) in &self.user_bindings {
            if !self.definitions.iter().any(|(id, _)| id == keybinding) {
                continue;
            }
            for key in normalize_keys(keys) {
                match claims.iter_mut().find(|(k, _)| *k == key) {
                    Some((_, claimants)) => {
                        if !claimants.contains(keybinding) {
                            claimants.push(keybinding.clone());
                        }
                    }
                    None => claims.push((key, vec![keybinding.clone()])),
                }
            }
        }
        for (key, keybindings) in claims {
            if keybindings.len() > 1 {
                self.conflicts.push(KeybindingConflict { key, keybindings });
            }
        }
        let resolved: Vec<(String, Vec<String>)> = self
            .definitions
            .iter()
            .map(|(id, definition)| (id.clone(), normalize_keys(self.user_keys(id).unwrap_or(&definition.default_keys))))
            .collect();
        self.keys_by_id = resolved.into_iter().collect();
    }

    /// Whether raw terminal `data` is one of the keys of `keybinding`.
    pub fn matches(&self, data: &str, keybinding: &str) -> bool {
        self.keys_by_id.get(keybinding).is_some_and(|keys| keys.iter().any(|key| matches_key(data, key)))
    }

    /// The resolved keys of `keybinding`.
    pub fn get_keys(&self, keybinding: &str) -> Vec<String> {
        self.keys_by_id.get(keybinding).cloned().unwrap_or_default()
    }

    /// The definition of `keybinding`, or `None` for an id the table does not define.
    pub fn get_definition(&self, keybinding: &str) -> Option<&KeybindingDefinition> {
        self.definitions.iter().find(|(id, _)| id == keybinding).map(|(_, d)| d)
    }

    /// The keys claimed by more than one user override.
    pub fn get_conflicts(&self) -> Vec<KeybindingConflict> {
        self.conflicts.clone()
    }

    /// Replace the user overrides and rebuild.
    pub fn set_user_bindings(&mut self, user_bindings: Vec<(String, Vec<String>)>) {
        self.user_bindings = user_bindings;
        self.rebuild();
    }

    /// A copy of the user overrides.
    pub fn get_user_bindings(&self) -> Vec<(String, Vec<String>)> {
        self.user_bindings.clone()
    }

    /// Every id to its resolved keys. Pi gives a lone key as a string and several as an array; here every entry is a key list.
    pub fn get_resolved_bindings(&self) -> Vec<(String, Vec<String>)> {
        self.definitions.iter().map(|(id, _)| (id.clone(), self.get_keys(id))).collect()
    }
}

static GLOBAL: Mutex<Option<Arc<KeybindingsManager>>> = Mutex::new(None);

/// pi-tui's `setKeybindings`: install the global manager.
pub fn set_keybindings(keybindings: KeybindingsManager) {
    *GLOBAL.lock().unwrap_or_else(|e| e.into_inner()) = Some(Arc::new(keybindings));
}

/// Forgets the installed manager, so [`get_keybindings`] answers the default again: the tests that read the default restore the process
/// state the tests that install a manager changed.
#[cfg(test)]
pub(crate) fn reset_keybindings() {
    *GLOBAL.lock().unwrap_or_else(|e| e.into_inner()) = None;
}

/// pi-tui's `getKeybindings`: the global manager, over [`tui_keybindings`] until one is installed.
pub fn get_keybindings() -> Arc<KeybindingsManager> {
    let mut guard = GLOBAL.lock().unwrap_or_else(|e| e.into_inner());
    guard.get_or_insert_with(|| Arc::new(KeybindingsManager::new(tui_keybindings(), Vec::new()))).clone()
}
