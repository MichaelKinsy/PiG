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

An extension can add models that choose a physical model for each request. See [Virtual models](virtual-models.md).

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

The Session selects and clamps the startup level before interactive mode opens. An explicit `--thinking` value takes precedence. A thinking suffix in `--model provider/model:level` also reaches the Session before interactive startup. Otherwise, a resumed Session keeps its saved level; a new Session uses the model preference, the global default, or `medium`. Unsupported levels round up to a supported level, or use the highest supported level if none is higher. The footer and editor show the Session's effective level, and provider requests use that same level through the model's thinking-level mapping. Opening interactive mode does not reset it from settings.

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

## Configure sampling by thinking level

OpenAI-compatible APIs support free-form `samplingParams` model defaults and `samplingParamsByThinkingLevel` overrides in `models.json`. The keys of `samplingParamsByThinkingLevel` are PiG thinking levels (`off`, `minimal`, `low`, `medium`, `high`, `xhigh` and `max`), not the provider values from `thinkingLevelMap`:

```json
{
  "id": "qwen-thinking-model",
  "reasoning": true,
  "samplingParams": {
    "temperature": 1.0,
    "top_p": 0.95
  },
  "samplingParamsByThinkingLevel": {
    "off": {
      "temperature": 0.7,
      "top_p": 0.8
    },
    "high": {
      "top_k": 20
    }
  }
}
```

PiG first clamps an unsupported thinking level to a supported one. It then merges the model's `samplingParams`, the effective level's entry and the request's `samplingParams`, in that order; a later value wins per key. A level without an entry uses the model defaults. A `modelOverrides` entry merges each level per key with the base model's entry. These fields apply only to `openai-completions`, `openai-responses` and `azure-openai-responses`; other APIs ignore them.

## Use classifier models

Classifier models do not chat. They answer typed questions about JSON state: pick one of several choices, answer yes or no, or give a score, each with probabilities. The built-in classifiers come from TypeSafe (Jev), Cloudflare Workers AI (Clef and Clef Flash), and OpenAI (GPT-6 Luna, through the [Decisions API](https://developers.openai.com/api/docs/guides/decisions)). The `openai` provider authenticates GPT-6 Luna with `OPENAI_API_KEY`.

OpenAI's Decisions API needs an API key. Sign in with ChatGPT credentials do not work with it, so `gpt-6-luna` is not listed as available while `openai` is logged in through `/login`, even when `OPENAI_API_KEY` is set; log out of `openai` to use the key. GPT-6 Luna also judges images passed in `images` (see `codemode.md`, "Classify"); other classifier models return an error for them. The API rejects inputs above 922K tokens, but requests that run longer than about five seconds, currently above roughly 600K input tokens, fail with a gateway timeout.

Classifier models do not appear in `/model`; the model reaches them through the `codemode` tool. Scripts list them with `models.getAvailableOfType("classifier")` and call `models.classify(model, { state, questions })`.

## Use image models

Image models generate images from a prompt and optional input images. PiG lists OpenRouter's image models, such as `google/gemini-2.5-flash-image` and `black-forest-labs/flux.2-pro`, under the `openrouter` provider; they use the same `OPENROUTER_API_KEY` or `/login` credential as its chat models.

Image models do not appear in `/model`; the model reaches them through the `codemode` tool. Scripts list them with `models.getAvailableOfType("image")` and call `models.generateImages(model, { input })`. The result's `output` holds base64 image blocks, which `image()` attaches to the `codemode` result so the model sees them:

```js
const painter = await models.getModelOfType("image", "openrouter", "google/gemini-2.5-flash-image");
const result = await models.generateImages(painter, {
  input: [{ type: "text", text: "A red fox in the snow, watercolor" }],
});
if (result.stopReason !== "stop") return result.errorMessage;
for (const block of result.output) if (block.type === "image") image(block);
```

`input` can also contain `{ type: "image", data, mimeType }` blocks to edit or use as references. PiG adds the usage of a script's image calls to the `codemode` tool result, like classifier calls. Generated images are not saved to disk. The codemode page (`codemode.md`, "Generate images") describes the full API.

Extensions generate images through `ctx.modelRegistry.generateImages()`, without codemode.

## Adding a model

New built-in models are generated into `ai/models_generated.go` and committed in source. Extensions cannot add new built-in models, but they can register an independent provider and model catalog. See [Providers](/docs/latest/providers) and [Extensions](/docs/latest/extensions).

## Provider cost tiers

An extension's registered model can include `cost.tiers`. Each tier supplies `inputTokensAbove` and the `input`, `output`, `cacheRead`, and `cacheWrite` rates per million tokens. PiG retains these thresholds and rates through Provider registration and model lookup. The existing cost calculation selects the applicable tier from total input usage.

## Common errors

Model configuration read, JSON parse, and schema errors include a `File:` line that identifies the selected `models.json` path. Check that file's contents and permissions. A missing file is allowed. PiG leaves invalid files unchanged.

| Symptom | Cause | Fix |
|---|---|---|
| `defaultModel` contains `github-copilot/gpt-6-sol`, but a new session starts on another model | The saved default expects separate provider and model fields. | Set `"defaultProvider": "github-copilot"` and `"defaultModel": "gpt-6-sol"`. |
| `model gpt-5.5 routed to openai but I selected copilot` | Bare ID passed to `BuildModel()` | Always pass `provider/id`. |
| `GPT-5.5 does not support thinking` after a binary swap | Stale `pig` on `PATH` | Reinstall or rebuild PiG, run `hash -r`, and restart the TUI. |
| Thinking cycle has no effect | Active model has `Reasoning: false` | Switch to a reasoning model. |
| Model selector empty after `pig login` | Credentials wrote to wrong root | Check `PIG_HOME` vs `~/.pig`. |
