# plan-mode extension

This opt-in Go extension is a partial port of Pi's plan-mode example. It provides read-only exploration, plan extraction, execution progress, `/plan`, `/todos`, and `ctrl+alt+p`. It is not accepted as a complete upstream-equivalent port.

## Unported behavior and landing dependencies

- **RPP-3, lone-surrogate strings:** Pi truncates step text with UTF-16 `slice(0, 47)`. A cut through a surrogate pair preserves a lone surrogate in JSON (for example, `\ud83d`). This Go example replaces that value with U+FFFD. Ordinary Go JSON decoding also loses lone surrogates in resumed state. A local display substitution or custom `TodoItem` marshaler cannot repair the SDK's string-valued message/UI calls and the host's decoded string values. The shared string/JSON boundary needs a reviewed representation and end-to-end persistence, message, UI, and resume evidence before this behavior is ported.
- **RPP-4, active-theme styling:** Status and todo widget strings do not apply Pi's `accent`, `warning`, `success`, and `muted` theme transformations. The host serves the active palette through `ui.theme`, but the Go SDK exposes only named-theme lookup (`GetTheme`) and does not expose the active theme. An all-SDK active-theme mechanism and conformance evidence are landing dependencies. Do not substitute a fixed palette or assume that a named theme is active.

These are unported behavior, not approved divergences. The plan-mode test mapping remains `pending`. Do not promote it on the basis of the passing tests below. Full TUI styling and execution-follow-up acceptance remain outstanding; the sibling print-command slice and inherited events root require their own reviewed landing.

## Verification

Run the example tests from this directory:

```bash
GOWORK=off go test -race ./...
GOWORK=off go test -run '^$' -bench '^BenchmarkPlanModeUtilities$' -benchmem
```

The utility corpus invokes Node against `.upstream/current` directly. It compares command patterns, JavaScript whitespace, BMP scalar casing, and Unicode plan extraction. It does not claim lone-surrogate round-trip fidelity.

Run the production RPC resume regressions from the repository root:

```bash
go test -race ./cmd/pig -run '^TestRPCPlanMode' -count=3
```

These tests launch the actual Go factory through the RPC Extension Host. They verify disk persistence, process restart, tool restoration on the next model turn, and completion reconstruction from multi-block assistant history. The example's other lifecycle tests use a framed SDK responder; they do not prove terminal rendering or real execution follow-ups.

## Use

Validate or activate the exact source directory:

```bash
pig install --validate-only --json examples/extensions/plan-mode
pig -e examples/extensions/plan-mode
```

The extension changes no Stock PiG defaults. Activate it explicitly with `-e`, extension policy, or a Piglet Resource.
