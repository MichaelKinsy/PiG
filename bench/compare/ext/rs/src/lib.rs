// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
//! Benchmark extension (Rust): a tool_call handler that does about 2 ms of work per call and counts its calls in $BENCHEXT_COUNTER.
use pig_sdk::{Extension, ToolResult};
use serde_json::json;
use std::io::Write;

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("bench-rs");
    ext.tool("hello_rs", "Return a hello greeting", json!({"type": "object"}), |_ctx, _p: serde_json::Value| {
        ToolResult::Json(json!({"content": "hello from rust"}))
    });
    ext.on_event("tool_call", |_ctx, _data| {
        let mut x: u64 = 0x9e3779b97f4a7c15;
        for i in 0..4_000_000u64 {
            x = x.rotate_left(5) ^ i.wrapping_mul(0x100000001b3);
        }
        if let Ok(path) = std::env::var("BENCHEXT_COUNTER") {
            if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(path) {
                let _ = writeln!(f, "rs {}", x & 1);
            }
        }
        None
    });
    ext
}
