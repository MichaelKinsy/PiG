//! Pi's `ProjectTrustStore` class and `hasTrustRequiringProjectResources` (core/trust-manager.ts). The host owns the store, so a decision saved here is the one the project trust gate reads. A decision is Pi's `ProjectTrustDecision`: `Some(true)`, `Some(false)` or `None` (undecided).

use crate::context::Context;
use crate::graph::{call_static, GraphObject};
use serde_json::{json, Value};
use std::io;

/// Pi's `ProjectTrustStoreEntry`: the nearest saved decision for a directory.
#[derive(Debug, Clone, PartialEq)]
pub struct ProjectTrustStoreEntry {
    pub path: String,
    pub decision: bool,
}

/// Pi's `ProjectTrustUpdate`. A `None` decision is Pi's null: it removes the saved decision of `path`.
#[derive(Debug, Clone, PartialEq)]
pub struct ProjectTrustUpdate {
    pub path: String,
    pub decision: Option<bool>,
}

/// Pi's `ProjectTrustStore`: the project trust decisions saved in `<agent_dir>/trust.json`.
pub struct ProjectTrustStore {
    object: GraphObject,
}

impl ProjectTrustStore {
    /// `new ProjectTrustStore(agentDir)`.
    pub fn new(context: &Context, agent_dir: &str) -> io::Result<ProjectTrustStore> {
        Ok(ProjectTrustStore { object: GraphObject::construct(context, "ProjectTrustStore", "new", vec![json!(agent_dir)])? })
    }

    /// The host's name for the store.
    pub fn handle(&self) -> &str {
        self.object.handle()
    }

    /// The nearest saved decision for `cwd` or one of its parents, `None` when there is none.
    pub fn get(&self, cwd: &str) -> io::Result<Option<bool>> {
        Ok(self.object.call("get", vec![json!(cwd)])?.as_bool())
    }

    /// The nearest saved decision with the directory that holds it, `None` when there is none.
    pub fn get_entry(&self, cwd: &str) -> io::Result<Option<ProjectTrustStoreEntry>> {
        let value = self.object.call("getEntry", vec![json!(cwd)])?;
        Ok(value.as_object().map(|entry| ProjectTrustStoreEntry {
            path: entry.get("path").and_then(Value::as_str).unwrap_or_default().to_string(),
            decision: entry.get("decision").and_then(Value::as_bool).unwrap_or(false),
        }))
    }

    /// Saves the decision for `cwd`; `None` removes it.
    pub fn set(&self, cwd: &str, decision: Option<bool>) -> io::Result<()> {
        self.object.call("set", vec![json!(cwd), decision.map_or(Value::Null, Value::Bool)]).map(|_| ())
    }

    /// Applies several decisions in one locked update of the store.
    pub fn set_many(&self, decisions: &[ProjectTrustUpdate]) -> io::Result<()> {
        let updates: Vec<Value> = decisions.iter().map(|update| json!({"path": update.path, "decision": update.decision})).collect();
        self.object.call("setMany", vec![Value::Array(updates)]).map(|_| ())
    }
}

/// Pi's `hasTrustRequiringProjectResources(cwd)`: whether `cwd` holds project-local resources that project trust gates (entries of its configuration directory, or `.agents/skills` in `cwd` or an ancestor other than the user's).
pub fn has_trust_requiring_project_resources(context: &Context, cwd: &str) -> io::Result<bool> {
    Ok(call_static(context, "ProjectTrustStore", "hasTrustRequiringProjectResources", vec![json!(cwd)])?.as_bool().unwrap_or(false))
}
