//! pi-ai's `Value` (TypeBox 1.3.27's `typebox/value`): [`Value::check`] and [`Value::errors`] of a schema, running the refinements (`Type::refine`) the schema carries.
//!
//! pig divergence (D226): TypeBox's walk runs in the host and the refinements are functions of this process, so the host asks for each refinement call it reaches and this
//! module answers it (`type.engine` members `Value.Check` and `Value.Errors`). Every refinement runs once per call, in the order TypeBox runs it. A refinement that fails
//! (returns `Err`, as a thrown JavaScript error does) fails the whole call with its message. The module is `pig_sdk::value`, not a root export: `serde_json::Value` is the
//! name extensions already import.

use serde_json::{Map, Value as Json, json};

use crate::type_engine::{CALL_TYPE_ENGINE, encode, encode_json};
use crate::type_schema::{Refinement, TSchema, TValue};

/// One error of [`Value::errors`], TypeBox's `TLocalizedValidationError`.
#[derive(Clone, Debug, PartialEq)]
pub struct ValueError {
    pub keyword: String,
    pub schema_path: String,
    pub instance_path: String,
    /// A JSON object, its members in the order TypeBox wrote them.
    pub params: Json,
    pub message: String,
}

/// TypeBox's `Value` namespace.
pub struct Value;

impl Value {
    /// `Value.Check(schema, value)`: whether `value` is valid for `schema`, running its refinements.
    pub fn check(schema: impl Into<TSchema>, value: &Json) -> Result<bool, String> {
        match call("Value.Check", schema.into(), value)? {
            Json::Bool(valid) => Ok(valid),
            other => Err(format!("Value.Check: the host answered {other}, not a boolean")),
        }
    }

    /// `Value.Errors(schema, value)`: the validation errors of `value` for `schema` (empty when it is valid), running its refinements.
    pub fn errors(schema: impl Into<TSchema>, value: &Json) -> Result<Vec<ValueError>, String> {
        let Json::Array(items) = call("Value.Errors", schema.into(), value)? else {
            return Err("Value.Errors: the host did not answer a list".to_string());
        };
        items.iter().map(error_of).collect()
    }
}

fn error_of(item: &Json) -> Result<ValueError, String> {
    let text = |name: &str| {
        item.get(name)
            .and_then(Json::as_str)
            .map(str::to_string)
            .ok_or_else(|| format!("Value.Errors: an error has no {name}"))
    };
    Ok(ValueError {
        keyword: text("keyword")?,
        schema_path: text("schemaPath")?,
        instance_path: text("instancePath")?,
        params: item
            .get("params")
            .cloned()
            .unwrap_or_else(|| Json::Object(Map::new())),
        message: text("message")?,
    })
}

fn call(member: &str, schema: TSchema, value: &Json) -> Result<Json, String> {
    let mut refs = Vec::new();
    let schema = encode(&TValue::Schema(schema), &mut refs);
    let value = encode_json(value);
    drive(member, schema, value, &refs, &mut |request| {
        crate::host_helpers::helper_call(CALL_TYPE_ENGINE, request)
            .map_err(|error| format!("{member}: {error}"))
    })
}

/// Ask the host through `send` until it answers a result, running the refinement each `call` reply names and sending the answers so far again.
fn drive(
    member: &str,
    schema: Json,
    value: Json,
    refs: &[Refinement],
    send: &mut dyn FnMut(Json) -> Result<Option<Json>, String>,
) -> Result<Json, String> {
    let mut answers: Vec<Json> = Vec::new();
    loop {
        let request = json!({"member": member, "args": [schema, value, answers]});
        let reply = send(request)?.unwrap_or(Json::Null);
        if let Some(error) = reply.get("error") {
            return Err(error
                .get("message")
                .and_then(Json::as_str)
                .unwrap_or_default()
                .to_string());
        }
        if let Some(wanted) = reply.get("call") {
            let id = wanted
                .get("id")
                .and_then(Json::as_u64)
                .ok_or_else(|| format!("{member}: the host asked for a refinement with no id"))?;
            let refinement = refs
                .get(id as usize)
                .ok_or_else(|| format!("{member}: the host asked for an unknown refinement"))?;
            let argument = wanted.get("value").unwrap_or(&Json::Null);
            let answer = if wanted.get("message").and_then(Json::as_bool) == Some(true) {
                match &refinement.error {
                    Some(error) => Json::String(error(argument)?),
                    None => Json::String("Refine Error".to_string()),
                }
            } else {
                Json::Bool((refinement.check)(argument)?)
            };
            answers.push(answer);
            continue;
        }
        return reply
            .get("value")
            .cloned()
            .ok_or_else(|| format!("{member}: the host answered no value"));
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::type_schema::Type;
    use std::sync::{Arc, Mutex};

    type Log = Arc<Mutex<Vec<String>>>;

    fn schema(log: &Log, throw: bool) -> (Json, Vec<Refinement>) {
        let (a, b, c) = (log.clone(), log.clone(), log.clone());
        let inner = Type::refine(
            Type::string(None),
            move |value| {
                a.lock().unwrap().push(format!("check:{value}"));
                if throw {
                    Err("kaboom".to_string())
                } else {
                    Ok(false)
                }
            },
            Some(Box::new(move |value| {
                b.lock().unwrap().push(format!("error:{value}"));
                Ok("too short".to_string())
            })),
        );
        let outer = Type::refine(
            inner,
            move |_| {
                c.lock().unwrap().push("check:outer".to_string());
                Ok(true)
            },
            None,
        );
        let mut refs = Vec::new();
        let wire = encode(&TValue::Schema(outer), &mut refs);
        (wire, refs)
    }

    fn scripted(
        replies: Vec<Json>,
        sent: &Arc<Mutex<Vec<Json>>>,
    ) -> impl FnMut(Json) -> Result<Option<Json>, String> {
        let mut replies = replies.into_iter();
        let sent = sent.clone();
        move |request| {
            sent.lock().unwrap().push(request);
            Ok(replies.next())
        }
    }

    #[test]
    fn each_refinement_runs_once_in_the_order_the_host_asks() {
        let log: Log = Default::default();
        let (wire, refs) = schema(&log, false);
        let sent = Arc::new(Mutex::new(Vec::new()));
        let mut send = scripted(
            vec![
                json!({"call": {"id": 0, "message": false, "value": "ab"}}),
                json!({"call": {"id": 0, "message": true, "value": "ab"}}),
                json!({"call": {"id": 1, "message": false, "value": "ab"}}),
                json!({"call": {"id": 1, "message": true, "value": "ab"}}),
                json!({"value": [{"keyword": "x"}]}),
            ],
            &sent,
        );
        let result = drive("Value.Errors", wire.clone(), json!("ab"), &refs, &mut send).unwrap();
        assert_eq!(result, json!([{"keyword": "x"}]));
        assert_eq!(
            *log.lock().unwrap(),
            vec!["check:\"ab\"", "error:\"ab\"", "check:outer"]
        );
        let sent = sent.lock().unwrap();
        let answers: Vec<&Json> = sent.iter().map(|request| &request["args"][2]).collect();
        assert_eq!(
            answers,
            vec![
                &json!([]),
                &json!([false]),
                &json!([false, "too short"]),
                &json!([false, "too short", true]),
                &json!([false, "too short", true, "Refine Error"]),
            ]
        );
        assert!(sent.iter().all(|request| request["member"] == "Value.Errors"
            && request["args"][0] == wire
            && request["args"][1] == json!("ab")));
    }

    #[test]
    fn a_failing_refinement_aborts_the_call() {
        let log: Log = Default::default();
        let (wire, refs) = schema(&log, true);
        let sent = Arc::new(Mutex::new(Vec::new()));
        let mut send = scripted(
            vec![
                json!({"call": {"id": 0, "message": false, "value": "ab"}}),
                json!({"value": true}),
            ],
            &sent,
        );
        let error = drive("Value.Check", wire, json!("ab"), &refs, &mut send).unwrap_err();
        assert_eq!(error, "kaboom");
        assert_eq!(sent.lock().unwrap().len(), 1);
        assert_eq!(log.lock().unwrap().len(), 1);
    }

    #[test]
    fn an_error_reply_fails_the_call() {
        let log: Log = Default::default();
        let (wire, refs) = schema(&log, false);
        let sent = Arc::new(Mutex::new(Vec::new()));
        let mut send = scripted(
            vec![json!({"error": {"name": "TypeError", "message": "bad schema"}})],
            &sent,
        );
        let error = drive("Value.Check", wire, json!(1), &refs, &mut send).unwrap_err();
        assert_eq!(error, "bad schema");
        assert!(log.lock().unwrap().is_empty());
    }

    #[test]
    fn errors_are_read_from_the_final_value() {
        let item = json!({"keyword": "refine", "schemaPath": "#", "instancePath": "/name", "params": {"b": 1, "a": 2}, "message": "m"});
        let error = error_of(&item).unwrap();
        assert_eq!(error.instance_path, "/name");
        assert_eq!(
            error.params.as_object().unwrap().keys().collect::<Vec<_>>(),
            ["b", "a"]
        );
    }
}
