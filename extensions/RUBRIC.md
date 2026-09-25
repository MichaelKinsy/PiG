# Extension Language Decision Rubric

When writing a new pig extension, use this rubric to decide between Go and Rust.

## Quick Decision

**Default to Go** unless ≥3 Rust factors apply.

## Go: Use When

| Factor | Why |
|--------|-----|
| **Session/context access** | Go extensions share types with Pig's `agent` package, such as `AgentMessage`. |
| **File system heavy** | `os.ReadFile`, `filepath.Walk`, `json.Unmarshal` are simpler than Rust equivalents. |
| **Prototype / iterate fast** | Faster compile (Go: ~1s, Rust: ~5-15s first build, ~2s incremental). |
| **Complex JSON handling** | `encoding/json` with `map[string]any` is ergonomic for dynamic shapes. Rust requires `serde_json::Value` gymnastics. |
| **Agent/LLM integration** | If the extension spawns sub-agents, reads session history, or modifies system prompts. |
| **UI-heavy (commands, widgets)** | Most UI work is string formatting + calling SDK methods. Go is simpler. |
| **First extension for a new author** | Lower learning curve. `go build` just works. |
| **Internal/personal tooling** | When the extension is for your own workflow, not distributed. |

### Go examples
- Sub-agent dispatch extensions are a good Go fit: they need session/context access, file I/O, JSON, and UI commands.

## Rust: Use When

| Factor | Why |
|--------|-----|
| **Hot-path text processing** | Regex, diff, patch parsing, AST walking. Rust's zero-cost abstractions win at scale. |
| **Replacing a built-in tool** | If the extension overrides `edit`, `bash`, etc. via `deduplicateTools()`, every invocation must be fast. |
| **Large input handling** | Processing multi-MB tool outputs, file contents, or streaming data. |
| **Memory-critical** | Rust's ownership model prevents leaks in long-running extensions. |
| **Binary size matters** | Rust binaries are ~2-4MB static. Go binaries are ~8-15MB. |
| **No GC pauses acceptable** | Widget rendering or tool execution where consistent latency matters. |
| **Complex algorithms** | Pattern matching, tree structures, graph algorithms: Rust's type system catches bugs at compile time. |
| **Cross-platform distribution** | If the extension will be distributed as a standalone binary. |

### Rust examples
- Multi-edit/diff extensions are a good Rust fit: they are hot-path text transforms that process large inputs.

## Decision Matrix

Score each factor 0 (not applicable) to 2 (strongly applies):

| Factor | Go score | Rust score |
|--------|----------|------------|
| Needs session/context types | +2 | 0 |
| Heavy file I/O / JSON | +2 | 0 |
| Hot-path text processing | 0 | +2 |
| Replaces built-in tool | 0 | +2 |
| Prototype speed matters | +1 | 0 |
| Large input (>1MB) processing | 0 | +1 |
| Complex algorithms | 0 | +1 |
| UI commands / widgets only | +1 | 0 |

**Sum the applicable scores.** Higher total wins. Ties → Go (lower friction).

## SDK Parity

Both SDKs expose the same 29-30 methods. No API is Go-only or Rust-only.
The wire protocol is identical: language choice is purely about the
extension's internal logic, not about what it can do.

| SDK | Methods | Tests | Build time (cached) |
|-----|---------|-------|---------------------|
| Go (`extensions/sdk/`) | 29 | 5 | ~0.5s |
| Rust (`extensions/sdk-rs/`) | 30 | 4 | ~2s |

## Anti-Patterns

- **Don't use Rust just because "it's faster."** Most extensions spend 99% of time waiting on IPC. The SDK call overhead (~1ms) dwarfs any language-level difference.
- **Don't use Go for CPU-bound text transforms.** A diff algorithm in Go will work but be 2-5x slower than Rust for large files.
- **Don't split one extension across both languages.** Each extension is one binary. If you need both Go and Rust strengths, either pick the dominant need or split into two extensions.
- **Don't choose based on personal preference alone.** The rubric exists to make the choice reproducible across team members.
