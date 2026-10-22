//! Event streams: the host sends the first payload of an event name and key whole and each later one as operations on the payload this connection last received for them (`EventStream` in the host's protocol.go). The reader applies a frame's operations as it reads it, in arrival order, and the request handler receives the full payload.

use std::collections::HashMap;

use serde_json::Value;

use crate::protocol::RequestMsg;

struct Replica {
    seq: u64,
    root: Value,
}

/// The payloads of the event streams one connection received, by event name and key (a `tool_execution_update` has one for each tool call); only its read loop touches it.
#[derive(Default)]
pub struct EventReplicas {
    replicas: HashMap<(String, String), Replica>,
}

impl EventReplicas {
    /// Replaces the request's args with the full payload its stream member produces, or records why it could not.
    pub fn apply(&mut self, req: &mut RequestMsg) {
        let Some(stream) = req.stream.clone() else { return };
        let name = req.event.clone().unwrap_or_default();
        let id = (name.clone(), stream.key.clone());
        let produced = match req.args.take() {
            Some(keyframe) => Ok(keyframe),
            None => match self.replicas.get_mut(&id) {
                Some(replica) if replica.seq == stream.base => {
                    let mut root = std::mem::take(&mut replica.root);
                    let ops = stream.ops.clone().unwrap_or(Value::Null);
                    apply_ops(&mut root, &ops).map(|()| root)
                }
                _ => Err(format!("{name} delta applies to payload {}, the replica holds a different one", stream.base)),
            },
        };
        match produced {
            Ok(root) => {
                req.args = Some(root.clone());
                self.replicas.insert(id, Replica { seq: stream.seq, root });
            }
            Err(message) => {
                self.replicas.remove(&id);
                req.stream_error = Some(message);
            }
        }
        for evicted in &stream.evict {
            self.replicas.remove(&(name.clone(), evicted.clone()));
        }
    }
}

pub(crate) fn apply_ops(root: &mut Value, ops: &Value) -> Result<(), String> {
    let list = ops.as_array().ok_or("event stream: ops are not an array")?;
    for op in list {
        let name = op.get("op").and_then(Value::as_str).ok_or("event stream: operation without a name")?;
        let path = op.get("path").and_then(Value::as_array).ok_or("event stream: bad path")?;
        let value = op.get("value").cloned().unwrap_or(Value::Null);
        apply_op(root, path, name, value)?;
    }
    Ok(())
}

fn apply_op(node: &mut Value, path: &[Value], op: &str, value: Value) -> Result<(), String> {
    let Some((first, rest)) = path.split_first() else {
        if op != "set" {
            return Err("event stream: bad root operation".into());
        }
        *node = value;
        return Ok(());
    };
    match node {
        Value::Object(members) => {
            let key = first.as_str().ok_or("event stream: bad path")?;
            if !rest.is_empty() {
                let child = members.get_mut(key).ok_or("event stream: bad path")?;
                return apply_op(child, rest, op, value);
            }
            match op {
                "set" => {
                    members.insert(key.to_string(), value);
                    Ok(())
                }
                "append" => append(members.get_mut(key), value),
                other => Err(format!("event stream: unknown operation {other:?}")),
            }
        }
        Value::Array(items) => {
            let index = first.as_u64().ok_or("event stream: bad path")? as usize;
            if index > items.len() {
                return Err("event stream: bad path".into());
            }
            if !rest.is_empty() {
                let child = items.get_mut(index).ok_or("event stream: bad path")?;
                return apply_op(child, rest, op, value);
            }
            match op {
                "set" if index == items.len() => {
                    items.push(value);
                    Ok(())
                }
                "set" => {
                    items[index] = value;
                    Ok(())
                }
                "append" => append(items.get_mut(index), value),
                other => Err(format!("event stream: unknown operation {other:?}")),
            }
        }
        _ => Err("event stream: bad path".into()),
    }
}

fn append(target: Option<&mut Value>, value: Value) -> Result<(), String> {
    match (target, value) {
        (Some(Value::String(current)), Value::String(suffix)) => {
            current.push_str(&suffix);
            Ok(())
        }
        _ => Err("event stream: append to a non-string".into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol::EventStream;
    use serde_json::json;

    fn request(seq: u64, base: u64, args: Option<Value>, ops: Option<Value>) -> RequestMsg {
        RequestMsg {
            method: "event".into(),
            tool: None,
            event: Some("message_update".into()),
            handler_id: 0,
            tool_call_id: None,
            batch: None,
            args,
            child_session: None, terminal_input: None,
            stream: Some(EventStream { seq, base, ops, key: String::new(), evict: Vec::new() }),
            stream_error: None,
            tool_call_index: None,
            args_raw: None,
        }
    }

    #[test]
    fn a_delta_applies_to_the_last_payload_and_the_handler_gets_its_own_copy() {
        let mut replicas = EventReplicas::default();
        let mut first = request(1, 0, Some(json!({"text": "ab", "list": [1], "nested": {"k": "v"}})), None);
        replicas.apply(&mut first);
        assert!(first.stream_error.is_none());
        // A handler edits its copy; the next delta must not see it.
        first.args.as_mut().unwrap()["text"] = json!("edited");
        let ops = json!([
            {"op": "append", "path": ["text"], "value": "cd"},
            {"op": "set", "path": ["list", 1], "value": 2},
            {"op": "set", "path": ["nested", "new"], "value": null},
            {"op": "set", "path": ["added"], "value": {"x": [true]}},
        ]);
        let mut second = request(2, 1, None, Some(ops));
        replicas.apply(&mut second);
        assert!(second.stream_error.is_none(), "{:?}", second.stream_error);
        assert_eq!(second.args, Some(json!({"text": "abcd", "list": [1, 2], "nested": {"k": "v", "new": null}, "added": {"x": [true]}})));
    }

    #[test]
    fn a_delta_for_another_payload_is_an_error_and_drops_the_replica() {
        let cases = [
            ("wrong base", 1, 7, json!([]), "delta applies to payload 7"),
            ("append to a number", 1, 1, json!([{"op": "append", "path": ["list", 0], "value": "x"}]), "append to a non-string"),
            ("missing member", 1, 1, json!([{"op": "set", "path": ["no", "where"], "value": 1}]), "bad path"),
            ("array past the end", 1, 1, json!([{"op": "set", "path": ["list", 9], "value": 1}]), "bad path"),
            ("unknown operation", 1, 1, json!([{"op": "del", "path": ["text"], "value": 1}]), "unknown operation"),
            ("ops not a list", 1, 1, json!({}), "ops are not an array"),
        ];
        for (name, _, base, ops, want) in cases {
            let mut replicas = EventReplicas::default();
            replicas.apply(&mut request(1, 0, Some(json!({"text": "x", "list": [1]})), None));
            let mut bad = request(2, base, None, Some(ops));
            replicas.apply(&mut bad);
            let message = bad.stream_error.unwrap_or_default();
            assert!(message.contains(want), "{name}: {message}");
            let mut after = request(3, 1, None, Some(json!([])));
            replicas.apply(&mut after);
            assert!(after.stream_error.is_some(), "{name}: the replica was kept after a failure");
        }
    }

    #[test]
    fn keys_are_independent_and_evicted() {
        let keyed = |key: &str, seq: u64, base: u64, args: Option<Value>, ops: Option<Value>, evict: &[&str]| {
            let mut req = request(seq, base, args, ops);
            req.event = Some("tool_execution_update".into());
            let stream = req.stream.as_mut().unwrap();
            stream.key = key.into();
            stream.evict = evict.iter().map(|k| k.to_string()).collect();
            req
        };
        let append = json!([{"op": "append", "path": ["text"], "value": "+"}]);
        let mut replicas = EventReplicas::default();
        for key in ["a", "b"] {
            replicas.apply(&mut keyed(key, 1, 0, Some(json!({"text": key})), None, &[]));
        }
        let mut b1 = keyed("b", 2, 1, None, Some(append.clone()), &[]);
        replicas.apply(&mut b1);
        assert_eq!(b1.args, Some(json!({"text": "b+"})));
        let mut a1 = keyed("a", 2, 1, None, Some(append.clone()), &[]);
        replicas.apply(&mut a1);
        assert_eq!(a1.args, Some(json!({"text": "a+"})));
        replicas.apply(&mut keyed("c", 1, 0, Some(json!({"text": "c"})), None, &["a"]));
        let mut stale = keyed("a", 3, 2, None, Some(append.clone()), &[]);
        replicas.apply(&mut stale);
        assert!(stale.stream_error.unwrap_or_default().contains("delta applies to payload 2"));
        let mut b2 = keyed("b", 3, 2, None, Some(append), &[]);
        replicas.apply(&mut b2);
        assert_eq!(b2.args, Some(json!({"text": "b++"})));
    }

    /// Sets or concatenates at `path` the plain way: the payload a delta produces by definition.
    fn reference_op(node: &mut Value, path: &[Value], op: &str, value: Value) {
        let (last, parents) = path.split_last().unwrap();
        let mut parent = node;
        for element in parents {
            parent = match element {
                Value::String(key) => &mut parent[key.as_str()],
                index => &mut parent[index.as_u64().unwrap() as usize],
            };
        }
        let slot = match last {
            Value::String(key) => parent.as_object_mut().unwrap().entry(key.clone()).or_insert(Value::Null),
            index => {
                let items = parent.as_array_mut().unwrap();
                let index = index.as_u64().unwrap() as usize;
                if index == items.len() {
                    items.push(Value::Null);
                }
                &mut items[index]
            }
        };
        *slot = match op {
            "append" => Value::String(format!("{}{}", slot.as_str().unwrap(), value.as_str().unwrap())),
            _ => value,
        };
    }

    #[test]
    fn random_deltas_match_concatenation() {
        let source: Vec<char> = "héllo wörld ✓ 日本語テキスト 🙂🚀 ascii tail ".chars().collect();
        let paths = [
            json!(["message", "content", 0, "text"]),
            json!(["message", "content", 1, "arguments", "content"]),
            json!(["message", "content", 1, "arguments", "path"]),
            json!(["assistantMessageEvent", "partial", "content", 0, "text"]),
        ];
        for seed in 1..=8u64 {
            let mut state = seed.wrapping_mul(0x9E37_79B9_7F4A_7C15) | 1;
            let mut next = |bound: usize| {
                state ^= state << 13;
                state ^= state >> 7;
                state ^= state << 17;
                (state % bound as u64) as usize
            };
            let keyframe = json!({"type": "message_update", "message": {"content": [{"type": "text", "text": "start "}, {"type": "toolCall", "arguments": {"content": "", "path": "a/"}}]}, "assistantMessageEvent": {"partial": {"content": [{"type": "text", "text": ""}]}, "list": []}});
            let mut reference = keyframe.clone();
            let mut replicas = EventReplicas::default();
            replicas.apply(&mut request(1, 0, Some(keyframe), None));
            let mut list_len = 0;
            for seq in 2..400u64 {
                let mut ops = Vec::new();
                for _ in 0..=next(4) {
                    let path = paths[next(paths.len())].clone();
                    let start = next(source.len());
                    let end = start + next(source.len() - start + 1);
                    let text: String = source[start..end].iter().collect();
                    ops.push(match next(20) {
                        0 => json!({"op": "set", "path": path, "value": text}),
                        1 => {
                            list_len += 1;
                            json!({"op": "set", "path": ["assistantMessageEvent", "list", list_len - 1], "value": "item"})
                        }
                        _ => json!({"op": "append", "path": path, "value": text}),
                    });
                }
                for op in &ops {
                    reference_op(&mut reference, op["path"].as_array().unwrap(), op["op"].as_str().unwrap(), op["value"].clone());
                }
                let mut req = request(seq, seq - 1, None, Some(Value::Array(ops)));
                replicas.apply(&mut req);
                assert!(req.stream_error.is_none(), "seed {seed} seq {seq}: {:?}", req.stream_error);
                let mut delivered = req.args.take().unwrap();
                assert_eq!(delivered, reference, "seed {seed} seq {seq}");
                // A handler that edits its payload does not change what the next delta applies to.
                delivered["message"]["content"][0]["text"] = json!("edited");
            }
        }
    }
}
