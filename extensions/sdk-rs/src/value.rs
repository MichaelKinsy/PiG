//! pi-ai's `Value` (TypeBox 1.3.27's `typebox/value`): [`Value::check`] and [`Value::errors`] of a schema, running the refinements (`Type::refine`) the schema carries.
//!
//! pig divergence (D226): TypeBox's walk runs in the host, suspended at each refinement call, and the refinements are functions of this process: this module starts the walk
//! (`type.engine` members `Value.Check` and `Value.Errors`) and answers each refinement call the host replies with (`Value.Answer`, or `Value.Cancel` when a refinement fails). Every
//! refinement runs once per call, in the order TypeBox runs it. A refinement that fails
//! (returns `Err`, as a thrown JavaScript error does) fails the whole call with its message. The module is `pig_sdk::value`, not a root export: `serde_json::Value` is the
//! name extensions already import.
//!
//! The Rust SDK's refinements return `bool` and `String` already, so there is no coercion of a refinement's result (the Python and Lisp SDKs coerce with their own truthiness).

use serde_json::{Map, Value as Json, json};

use crate::type_engine::{CALL_TYPE_ENGINE, encode, encode_json};
use crate::type_schema::{Refinement, TSchema, TValue};

/// One error of [`Value::errors`], TypeBox's `TLocalizedValidationError`.
#[derive(Clone, Debug, PartialEq)]
pub struct TLocalizedValidationError {
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
    /// `Value.Check(schema, value)`: whether `value` is valid for `schema`, running its refinements. A refinement that returns `Err` fails the call with its message.
    pub fn check(schema: impl Into<TSchema>, value: &Json) -> Result<bool, String> {
        match call("Value.Check", schema.into(), value)? {
            Json::Bool(valid) => Ok(valid),
            other => Err(format!(
                "Value.Check: the host answered {other}, not a boolean"
            )),
        }
    }

    /// `Value.Errors(schema, value)`: the validation errors of `value` for `schema` (empty when it is valid), running its refinements.
    pub fn errors(
        schema: impl Into<TSchema>,
        value: &Json,
    ) -> Result<Vec<TLocalizedValidationError>, String> {
        let Json::Array(items) = call("Value.Errors", schema.into(), value)? else {
            return Err("Value.Errors: the host did not answer a list".to_string());
        };
        items.iter().map(error_of).collect()
    }
}

fn error_of(item: &Json) -> Result<TLocalizedValidationError, String> {
    let text = |name: &str| {
        item.get(name)
            .and_then(Json::as_str)
            .map(str::to_string)
            .ok_or_else(|| format!("Value.Errors: an error has no {name}"))
    };
    Ok(TLocalizedValidationError {
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

/// Start `member` through `send` and answer each `call` the host replies with the result of the refinement it names, until the host answers a result. The host keeps the walk
/// suspended between the requests: the schema and the value are sent once, then only each answer with the `token` of the call. A refinement that fails, or any other early exit
/// after the host gave a token, cancels the call (best effort) and fails with the reason; an `error` reply ends the call, the host having dropped the token.
fn drive(
    member: &str,
    schema: Json,
    value: Json,
    refs: &[Refinement],
    send: &mut dyn FnMut(Json) -> Result<Option<Json>, String>,
) -> Result<Json, String> {
    let mut request = json!({"member": member, "args": [schema, value]});
    loop {
        let reply = send(request)?.unwrap_or(Json::Null);
        if let Some(error) = reply.get("error") {
            return Err(error
                .get("message")
                .and_then(Json::as_str)
                .unwrap_or_default()
                .to_string());
        }
        let Some(wanted) = reply.get("call") else {
            return reply
                .get("value")
                .cloned()
                .ok_or_else(|| format!("{member}: the host answered no value"));
        };
        let Some(token) = wanted.get("token").and_then(Json::as_str) else {
            return Err(format!(
                "{member}: the host asked for a refinement with no token"
            ));
        };
        let cancel = |send: &mut dyn FnMut(Json) -> Result<Option<Json>, String>| {
            let _ = send(json!({"member": "Value.Cancel", "args": [token]}));
        };
        let refinement = wanted
            .get("id")
            .and_then(Json::as_u64)
            .and_then(|id| refs.get(id as usize));
        let Some(refinement) = refinement else {
            cancel(send);
            return Err(format!(
                "{member}: the host asked for an unknown refinement"
            ));
        };
        let argument = wanted.get("value").unwrap_or(&Json::Null);
        // A refinement that panics cancels the host's parked walk before the panic goes on: the host would otherwise hold it until the connection closes.
        let ran = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            if wanted.get("message").and_then(Json::as_bool) == Some(true) {
                match &refinement.error {
                    Some(error) => error(argument).map(Json::String),
                    None => Ok(Json::String("Refine Error".to_string())),
                }
            } else {
                (refinement.check)(argument).map(Json::Bool)
            }
        }));
        let answer = match ran {
            Ok(answer) => answer,
            Err(panic) => {
                cancel(send);
                std::panic::resume_unwind(panic);
            }
        };
        match answer {
            Ok(answer) => request = json!({"member": "Value.Answer", "args": [token, answer]}),
            Err(failure) => {
                cancel(send);
                return Err(failure);
            }
        }
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
                json!({"call": {"token": "t1", "id": 0, "message": false, "value": "ab"}}),
                json!({"call": {"token": "t1", "id": 0, "message": true, "value": "ab"}}),
                json!({"call": {"token": "t1", "id": 1, "message": false, "value": "ab"}}),
                json!({"call": {"token": "t1", "id": 1, "message": true, "value": "ab"}}),
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
        assert_eq!(
            *sent,
            vec![
                json!({"member": "Value.Errors", "args": [wire, "ab"]}),
                json!({"member": "Value.Answer", "args": ["t1", false]}),
                json!({"member": "Value.Answer", "args": ["t1", "too short"]}),
                json!({"member": "Value.Answer", "args": ["t1", true]}),
                json!({"member": "Value.Answer", "args": ["t1", "Refine Error"]}),
            ]
        );
    }

    #[test]
    fn a_failing_refinement_cancels_and_aborts_the_call() {
        let log: Log = Default::default();
        let (wire, refs) = schema(&log, true);
        let sent = Arc::new(Mutex::new(Vec::new()));
        let mut send = scripted(
            vec![
                json!({"call": {"token": "t9", "id": 0, "message": false, "value": "ab"}}),
                json!({"value": true}),
            ],
            &sent,
        );
        let error = drive("Value.Check", wire.clone(), json!("ab"), &refs, &mut send).unwrap_err();
        assert_eq!(error, "kaboom");
        assert_eq!(log.lock().unwrap().len(), 1);
        assert_eq!(
            *sent.lock().unwrap(),
            vec![
                json!({"member": "Value.Check", "args": [wire, "ab"]}),
                json!({"member": "Value.Cancel", "args": ["t9"]}),
            ]
        );
    }

    #[test]
    fn an_error_reply_fails_the_call_without_a_cancel() {
        let log: Log = Default::default();
        let (wire, refs) = schema(&log, false);
        let sent = Arc::new(Mutex::new(Vec::new()));
        let mut send = scripted(
            vec![
                json!({"call": {"token": "t2", "id": 0, "message": false, "value": "ab"}}),
                json!({"error": {"name": "RangeError", "message": "Maximum call stack size exceeded"}}),
            ],
            &sent,
        );
        let error = drive("Value.Check", wire, json!("ab"), &refs, &mut send).unwrap_err();
        assert_eq!(error, "Maximum call stack size exceeded");
        assert_eq!(sent.lock().unwrap().len(), 2);
        assert_eq!(log.lock().unwrap().len(), 1);
    }

    #[test]
    fn an_error_reply_to_the_first_request_fails_the_call() {
        let log: Log = Default::default();
        let (wire, refs) = schema(&log, false);
        let sent = Arc::new(Mutex::new(Vec::new()));
        let mut send = scripted(
            vec![json!({"error": {"name": "TypeError", "message": "bad schema"}})],
            &sent,
        );
        let error = drive("Value.Check", wire, json!(1), &refs, &mut send).unwrap_err();
        assert_eq!(error, "bad schema");
        assert_eq!(sent.lock().unwrap().len(), 1);
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

    #[test]
    fn a_panicking_refinement_cancels_the_walk_and_the_panic_goes_on() {
        let log: Log = Default::default();
        let (wire, refs) = schema(&log, false);
        let refs: Vec<Refinement> = refs
            .into_iter()
            .map(|mut refinement| {
                refinement.check = Arc::new(|_| panic!("boom"));
                refinement
            })
            .collect();
        let sent = Arc::new(Mutex::new(Vec::new()));
        let mut send = scripted(
            vec![json!({"call": {"token": "t1", "id": 0, "message": false, "value": "ab"}})],
            &sent,
        );
        let outcome = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            let _ = drive("Value.Check", wire.clone(), json!("ab"), &refs, &mut send);
        }));
        assert!(outcome.is_err(), "the panic must reach the caller");
        let sent = sent.lock().unwrap();
        assert_eq!(sent.len(), 2, "{sent:?}");
        assert_eq!(sent[1], json!({"member": "Value.Cancel", "args": ["t1"]}));
    }
}
