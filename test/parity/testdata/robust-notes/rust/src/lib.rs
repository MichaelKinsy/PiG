//! The Rust SDK version of the robust-notes fixture (../node/index.mjs).

use pig_sdk::{CommandResult, Context, Extension, FlagOptions, JsString, RemoteComponent, RemoteComponentResult, ToolResult};
use serde_json::{Value, json};
use std::sync::atomic::{AtomicUsize, Ordering};

struct Picker {
    items: Vec<&'static str>,
    selected: usize,
}

impl RemoteComponent for Picker {
    fn render(&self, width: u32) -> Vec<String> {
        let mut lines = vec![format!("robust pick ({width})")];
        for (index, item) in self.items.iter().enumerate() {
            lines.push(format!("{} {item}", if index == self.selected { ">" } else { " " }));
        }
        lines
    }

    fn handle_input(&mut self, data: &JsString) -> Result<RemoteComponentResult, String> {
        if *data == "j" {
            self.selected = (self.selected + 1) % self.items.len();
        } else if *data == "s" {
            return Ok(RemoteComponentResult::done(Some(json!(self.items[self.selected]))));
        } else if *data == "q" {
            return Ok(RemoteComponentResult::done(None));
        }
        Ok(RemoteComponentResult::pending())
    }
}

fn flag(ctx: &Context) -> String {
    match ctx.get_flag("robust-prefix") {
        Ok(Some(Value::String(prefix))) => prefix,
        Ok(Some(other)) => other.to_string(),
        _ => "undefined".to_string(),
    }
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("rust");
    static TOOL_RUNS: AtomicUsize = AtomicUsize::new(0);
    ext.flag("robust-prefix", FlagOptions::string("Prefix for robust_words output", "rw"));
    ext.tool(
        "robust_words",
        "Split a command line into words.",
        json!({"type": "object", "properties": {"line": {"type": "string"}}, "required": ["line"]}),
        |ctx, params| {
            let line = params.get("line").and_then(Value::as_str).unwrap_or_default();
            match shlex::split(line) {
                Some(words) => ToolResult::text(format!("{}: {}", flag(ctx), words.join("|"))),
                None => ToolResult::Error("unterminated quote".to_string()),
            }
        },
    );
    ext.on_event("session_start", false, |ctx, _data| {
        ctx.set_status("robust", "robust: ready");
        None
    });
    ext.on_event("tool_result", false, |ctx, data| {
        if data.get("toolName").and_then(Value::as_str) != Some("robust_words") {
            return None;
        }
        let runs = TOOL_RUNS.fetch_add(1, Ordering::SeqCst) + 1;
        ctx.set_status("robust", &format!("robust: tools={runs}"));
        None
    });
    ext.command("robust-settings", "Show the robust-notes settings", |ctx, _args| {
        let settings = match ctx.get_settings() {
            Ok(settings) => settings,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let greeting = settings.pointer("/robustNotes/greeting").and_then(Value::as_str).unwrap_or("none").to_string();
        ctx.notify(&format!("robust settings: greeting={greeting} prefix={}", flag(ctx)), "info");
        CommandResult::Ok
    });
    ext.command("robust-note", "Save a note", |ctx, args| {
        if let Err(err) = ctx.append_entry("robust-note", json!({"text": args})) {
            return CommandResult::Error(err.to_string());
        }
        let entries = match ctx.get_entries() {
            Ok(entries) => entries,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let notes = entries
            .iter()
            .filter(|entry| entry.get("type").and_then(Value::as_str) == Some("custom") && entry.get("customType").and_then(Value::as_str) == Some("robust-note"))
            .count();
        ctx.notify(&format!("robust note {notes}: {args}"), "info");
        CommandResult::Ok
    });
    ext.command("robust-pick", "Pick an item in an overlay", |ctx, _args| {
        let choice = match ctx.custom_component(Picker { items: vec!["alpha", "beta", "gamma"], selected: 0 }, json!({"overlay": true})) {
            Ok(choice) => choice,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let text = match choice {
            Some(Value::String(choice)) => choice,
            Some(Value::Null) | None => "nothing".to_string(),
            Some(other) => other.to_string(),
        };
        ctx.notify(&format!("robust picked: {text}"), "info");
        CommandResult::Ok
    });
    ext
}
