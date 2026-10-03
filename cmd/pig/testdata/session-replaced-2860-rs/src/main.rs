//! session-replaced-2860-rs runs the bodies of Pi's 2860-replaced-session-context.test.ts cases as Rust SDK commands.
//! Each line goes to the file named by PIG_TEST_2860_LOG; process ids name the extension instances, because each
//! Session's extensions run in their own process here.
use pig_sdk::{CommandResult, Extension, ReplacedSessionContext};
use serde_json::{Value, json};
use std::io::Write;

fn record(line: &str) {
    if let Ok(path) = std::env::var("PIG_TEST_2860_LOG")
        && let Ok(mut file) = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(path)
    {
        let _ = writeln!(file, "{line}");
    }
}

fn text(content: &Value) -> String {
    match content {
        Value::String(text) => text.clone(),
        Value::Array(parts) => parts
            .iter()
            .filter(|part| part["type"] == "text")
            .filter_map(|part| part["text"].as_str())
            .collect(),
        _ => String::new(),
    }
}

fn conversation(ctx: &pig_sdk::Context) -> String {
    let branch = match ctx.session_manager().get_branch(None) {
        Ok(branch) => branch,
        Err(error) => return format!("error:{error}"),
    };
    branch
        .as_array()
        .into_iter()
        .flatten()
        .filter(|entry| entry["type"] == "message" && entry["message"]["role"] != "system")
        .map(|entry| {
            format!(
                "{}:{}",
                entry["message"]["role"].as_str().unwrap_or_default(),
                text(&entry["message"]["content"])
            )
        })
        .collect::<Vec<_>>()
        .join("|")
}

fn marks() -> serde_json::Map<String, Value> {
    std::env::var("PIG_TEST_2860_MARKS")
        .ok()
        .and_then(|path| std::fs::read_to_string(path).ok())
        .and_then(|data| serde_json::from_str(&data).ok())
        .unwrap_or_default()
}

fn session_file(ctx: &pig_sdk::Context) -> Option<String> {
    ctx.session_manager()
        .get_session_file()
        .ok()
        .and_then(|file| file.as_str().map(str::to_string))
}

fn result(outcome: std::io::Result<Option<Value>>) -> CommandResult {
    match outcome {
        Ok(_) => CommandResult::Ok,
        Err(error) => CommandResult::Error(error.to_string()),
    }
}

fn main() {
    let pid = std::process::id();
    let mut ext = Extension::new("session-replaced-2860-rs");
    ext.on_event("session_start", false, move |_, _| {
        record(&format!("start:{pid}"));
        None
    });
    ext.on_event("session_shutdown", false, move |_, _| {
        record(&format!("shutdown:{pid}"));
        None
    });
    ext.command("repro", "repro", move |ctx, _| {
        let old_ctx = ctx.clone();
        let old_session_file = session_file(ctx);
        result(ctx.new_session_with(
            json!({"parentSession": old_session_file}),
            move |replaced: &ReplacedSessionContext| {
                record(&format!("with:{pid}"));
                let replacement = session_file(replaced);
                record(&format!(
                    "replacement:{}",
                    replacement.is_some() && replacement != old_session_file
                ));
                record(&format!(
                    "staleCtx:{}",
                    old_ctx.session_manager().get_session_file().is_err()
                ));
                record(&format!(
                    "stalePi:{}",
                    old_ctx.send_user_message("stale message", "").is_err()
                ));
                replaced.send_user_message("reply with exactly: hello reply", "")?;
                record(&format!("idle:{}", replaced.is_idle()?));
                record(&format!("model:{}", replaced.model()));
                record(&format!("conversation:{}", conversation(replaced)));
                Ok(())
            },
        ))
    });
    ext.command("throw-it", "throw-it", |ctx, _| {
        let outcome = ctx.new_session_with(json!({}), |_: &ReplacedSessionContext| {
            Err(std::io::Error::new(
                std::io::ErrorKind::InvalidInput,
                "callback failed",
            ))
        });
        match outcome {
            Ok(_) => record("caught:none"),
            Err(error) if error.kind() == std::io::ErrorKind::InvalidInput => {
                record(&format!("caught:{error}"))
            }
            Err(error) => record(&format!("caught:other:{error}")),
        }
        CommandResult::Ok
    });
    // A context kept past its callback must not block the extension: the command still returns.
    ext.command("keep-it", "keep-it", |ctx, _| {
        let kept = std::sync::Arc::new(std::sync::Mutex::new(None));
        let slot = kept.clone();
        if let Err(error) = ctx.new_session_with(json!({}), move |replaced: &ReplacedSessionContext| {
            *slot.lock().unwrap() = Some(replaced.context().clone());
            Ok(())
        }) {
            return CommandResult::Error(error.to_string());
        }
        if let Some(kept) = kept.lock().unwrap().take() {
            let _ = kept.session_manager().get_session_file();
            let _ = kept.is_idle();
            let _ = kept.send_user_message("kept message", "");
        }
        record("kept:returned");
        CommandResult::Ok
    });
    ext.command("fork-it", "fork-it", |ctx, _| {
        let leaf = match ctx.session_manager().get_leaf_id() {
            Ok(Value::String(leaf)) => leaf,
            Ok(_) => return CommandResult::Error("Missing leaf id".to_string()),
            Err(error) => return CommandResult::Error(error.to_string()),
        };
        result(ctx.fork_with(
            &leaf,
            json!({"position": "at"}),
            |replaced: &ReplacedSessionContext| {
                replaced.send_user_message("reply with exactly: fork reply", "")?;
                record(&format!("conversation:{}", conversation(replaced)));
                Ok(())
            },
        ))
    });
    ext.command("mark", "mark", |ctx, name| {
        let mut values = marks();
        values.insert(
            name.to_string(),
            session_file(ctx).map(Value::String).unwrap_or(Value::Null),
        );
        match std::fs::write(
            std::env::var("PIG_TEST_2860_MARKS").unwrap_or_default(),
            Value::Object(values).to_string(),
        ) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("new", "new", |ctx, _| result(ctx.new_session(json!({}))));
    ext.command("switch", "switch", |ctx, name| {
        let target = marks()
            .get(name)
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string();
        result(ctx.switch_session(&target, json!({})))
    });
    ext.command("switch-it", "switch-it", |ctx, _| {
        let target = marks()
            .get("target")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string();
        let expected = target.clone();
        result(ctx.switch_session_with(
            &target,
            json!({}),
            move |replaced: &ReplacedSessionContext| {
                replaced.send_user_message("reply with exactly: switch reply", "")?;
                record(&format!(
                    "switched:{}",
                    session_file(replaced).as_deref() == Some(expected.as_str())
                ));
                record(&format!("conversation:{}", conversation(replaced)));
                Ok(())
            },
        ))
    });
    if let Err(error) = ext.run() {
        eprintln!("session-replaced-2860-rs: {error}");
        std::process::exit(1);
    }
}
