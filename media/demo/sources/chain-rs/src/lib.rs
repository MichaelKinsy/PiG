//! chain-rs: the `chain` Pi extension, ported to Rust for a Piglet Binary.
//!
//!   /chain fix the failing tests
//!
//! Runs the task as a chain of agent steps (scout -> plan -> build -> test)
//! and shows a progress widget above the editor. Same command, same prompts,
//! same widget as the TypeScript `chain` extension.
use pig_sdk::{CommandResult, Extension};
use serde_json::Value;
use std::sync::{Arc, Mutex};
use std::time::Instant;

const STEPS: [(&str, &str); 4] = [
    ("scout", "Scout the repository for code relevant to: {t}. Report findings only."),
    ("plan", "Write a short numbered plan to: {t}."),
    ("build", "Implement the plan to: {t}. Make the smallest correct change."),
    ("test", "Run the tests and report the result for: {t}."),
];

struct Run {
    task: String,
    step: usize,
    running: bool,
    tools: usize,
    t0: Instant,
}

fn fg(rgb: (u8, u8, u8), s: &str) -> String {
    format!("\x1b[38;2;{};{};{}m{}\x1b[39m", rgb.0, rgb.1, rgb.2, s)
}
const ACCENT: (u8, u8, u8) = (138, 190, 183);
const OK: (u8, u8, u8) = (181, 189, 104);
const DIM: (u8, u8, u8) = (102, 102, 102);
const CRAB: (u8, u8, u8) = (222, 165, 132);

fn render(ctx: &pig_sdk::Context, run: &Run) {
    let parts: Vec<String> = STEPS
        .iter()
        .enumerate()
        .map(|(i, (name, _))| {
            if i < run.step {
                fg(OK, &format!("✓ {name}"))
            } else if i == run.step && run.running {
                fg(ACCENT, &format!("◐ {name}"))
            } else {
                fg(DIM, &format!("○ {name}"))
            }
        })
        .collect();
    let secs = run.t0.elapsed().as_secs_f64();
    let line = format!(
        " {}  {}   {}",
        fg(ACCENT, "⛓ chain") + &fg(CRAB, " (rust)"),
        parts.join(&fg(DIM, " ── ")),
        fg(DIM, &format!("{secs:.1}s · {} tools", run.tools))
    );
    let _ = ctx.set_widget("chain", vec![line]);
}

fn send(ctx: &pig_sdk::Context, run: &Run) {
    let (name, prompt) = STEPS[run.step];
    let text = format!(
        "[chain {}/{} · {}] {}",
        run.step + 1,
        STEPS.len(),
        name,
        prompt.replace("{t}", &run.task)
    );
    let _ = ctx.send_user_message(text, "followUp");
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("chain-rs");
    let state: Arc<Mutex<Option<Run>>> = Arc::new(Mutex::new(None));

    let s = state.clone();
    ext.command("chain", "Run a task as a chain: scout -> plan -> build -> test", move |ctx, args: &str| {
        let task = args.trim();
        if task.is_empty() {
            ctx.notify("Usage: /chain <task>", "warning");
            return CommandResult::Ok;
        }
        let mut guard = s.lock().unwrap();
        if guard.is_some() {
            ctx.notify("A chain is already running", "warning");
            return CommandResult::Ok;
        }
        let run = Run { task: task.to_string(), step: 0, running: true, tools: 0, t0: Instant::now() };
        render(ctx, &run);
        send(ctx, &run);
        *guard = Some(run);
        CommandResult::Ok
    });

    let s = state.clone();
    ext.on_event("agent_start", false, move |ctx, _data: &mut Value| {
        if let Some(run) = s.lock().unwrap().as_mut() {
            run.running = true;
            render(ctx, run);
        }
        None
    });

    let s = state.clone();
    ext.on_event("tool_execution_start", false, move |ctx, _data: &mut Value| {
        if let Some(run) = s.lock().unwrap().as_mut() {
            run.tools += 1;
            render(ctx, run);
        }
        None
    });

    let s = state.clone();
    ext.on_event("agent_end", false, move |ctx, _data: &mut Value| {
        let mut guard = s.lock().unwrap();
        let Some(run) = guard.as_mut() else { return None };
        run.step += 1;
        run.running = false;
        render(ctx, run);
        if run.step >= STEPS.len() {
            let secs = run.t0.elapsed().as_secs_f64();
            ctx.notify(&format!("chain finished: {} steps in {secs:.1}s", STEPS.len()), "info");
            *guard = None;
        } else {
            send(ctx, run);
        }
        None
    });

    ext
}
