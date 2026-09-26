# Models

A model in PiG is identified by a **provider-qualified spec**: `provider/modelID`. Model-selection APIs such as `--model`, `/model`, `Ctrl+P` cycling, and `Context.SetModel()` use provider-qualified specs. Scoped model entries in settings also use that form. The saved default is an exception: set `defaultProvider` to the provider and `defaultModel` to the model ID without the provider prefix. A provider-qualified `defaultModel` does not select that model at startup. Bare model IDs passed to model-selection APIs are accepted for backward compatibility but can route to `openai/<id>` instead of the intended provider.

## Selecting a model

| Mechanism | Effect |
|---|---|
| `pig --model openai/gpt-5.5` | Override for this process. |
| `/model` slash command | Open the selector. Enter selects for this session. Ctrl+S selects and saves the default. |
| `Ctrl+P` / `Shift+Ctrl+P` | Cycle next/previous within scoped models. |
| `/scoped-models` | Edit the cycle list interactively. |
| `--models a,b,c` | Set scope for this run only (does not persist). |
| `setModel(spec)` (extension API) | Programmatic switch. Returns `(applied, error)`. |

The selector lists models from providers with configured authentication. It uses `GEMINI_API_KEY` for Google Gemini, not `GOOGLE_API_KEY`. Providers without configured authentication are omitted.

## Scoped models

Scoped models are the list `Ctrl+P` cycles through. They are stored in settings as provider-qualified IDs; PiG accepts legacy bare IDs and rewrites them on next save.

Cycling preserves both `provider` and `id`. The `generatedModelSpec` helper produces specs like:

- `github-copilot/gpt-5.5`
- `openai/gpt-5.5-mini`
- `openrouter/openai/gpt-5.5` - trailing slash inside `id` is preserved.

If a custom helper or extension passes only `model.ID` to `BuildModel()`, PiG falls back to `openai/<id>` - never do this in cycling code paths.

## Thinking levels

PiG surfaces reasoning effort through these named levels:

```text
off → minimal → low → medium → high → xhigh → max
```

A model exposes only the levels it supports. Cycling follows that supported set.

- `Shift+Tab` cycles thinking on models that advertise reasoning support.
- `getThinkingLevel()` and `setThinkingLevel(level)` are exposed to extensions.
- The current label is rendered in the status line; extensions can override the hidden label with `ui.setHiddenThinkingLabel`.
- Models that do not support reasoning silently ignore changes; the host does not emit an error.

A model is reasoning-capable when its generated entry sets `Reasoning: true`; `ThinkingLevelMap` controls how each level is wired into the request. See `ai/models_generated.go` in source for the table.

## Model metadata

`getAllTools()` excluded, every model entry the host knows about carries:

- `Provider` - provider key (see `providers.md`).
- `ID` - model identifier as the provider names it. May contain slashes.
- `DisplayName` - UI label.
- `Reasoning` - bool; whether the model supports `thinking_level`.
- `ThinkingLevelMap` - provider-specific wiring per level.
- `MaxTokens` / `ContextWindow` - for context usage math.
- `Cost` (if known) - input/output rates.

Extensions can read this via `getModelInfo()` (returns `*ModelInfo`) inside any handler.

## Adding a model

New built-in models are generated into `ai/models_generated.go` and committed in source. Extensions cannot add new built-in models, but they can register an independent provider and model catalog. See [Providers](/docs/latest/providers) and [Extensions](/docs/latest/extensions).

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `defaultModel` contains `github-copilot/gpt-6-sol`, but a new session starts on another model | The saved default expects separate provider and model fields. | Set `"defaultProvider": "github-copilot"` and `"defaultModel": "gpt-6-sol"`. |
| `model gpt-5.5 routed to openai but I selected copilot` | Bare ID passed to `BuildModel()` | Always pass `provider/id`. |
| `GPT-5.5 does not support thinking` after a binary swap | Stale `pig` on `PATH` | Reinstall or rebuild PiG, run `hash -r`, and restart the TUI. |
| Thinking cycle has no effect | Active model has `Reasoning: false` | Switch to a reasoning model. |
| Model selector empty after `pig login` | Credentials wrote to wrong root | Check `PIG_HOME` vs `~/.pig`. |
