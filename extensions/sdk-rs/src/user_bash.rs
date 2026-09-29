use serde_json::{Value, json};

// Native null represents the number-or-undefined exit code. Preserve its property presence before JSON encoding.
pub(crate) fn user_bash_event_result(mut value: Option<Value>) -> Option<Value> {
    if let Some(Value::Object(object)) = value.as_mut() {
        if let Some(Value::Object(result)) = object.get_mut("result") {
            let undefined = result.get("exitCode").is_some_and(Value::is_null);
            if undefined {
                result.remove("exitCode");
            }
            if result.get("fullOutputPath").is_some_and(Value::is_null) {
                result.remove("fullOutputPath");
            }
            object.insert("_pigUserBashExitCodeUndefined".into(), json!(undefined));
        }
    }
    value
}
