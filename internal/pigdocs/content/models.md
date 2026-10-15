# Models

A model in pig is identified by a **provider-qualified spec**: `provider/modelID`. Model-selection APIs such as `--model`, `/model`, `Ctrl+P` cycling, and `Context.SetModel()` use provider-qualified specs. Scoped model entries in settings also use that form. The saved default is an exception: set `defaultProvider` to the provider and `defaultModel` to the model ID without the provider prefix. A provider-qualified `defaultModel` does not select that model at startup. Bare model IDs passed to model-selection APIs are accepted for backward compatibility but can route to `openai/<id>` instead of the intended provider.

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

Scoped models are the list `Ctrl+P` cycles through. They are stored in settings as provider-qualified IDs; pig accepts legacy bare IDs and rewrites them on next save.

Cycling preserves both `provider` and `id`. The `generatedModelSpec` helper produces specs like:

- `github-copilot/gpt-5.5`
- `openai/gpt-5.5-mini`
- `openrouter/openai/gpt-5.5` - trailing slash inside `id` is preserved.

If a custom helper or extension passes only `model.ID` to `BuildModel()`, pig falls back to `openai/<id>` - never do this in cycling code paths.

## Thinking levels

PiG surfaces reasoning effort through these named levels:

```
off → minimal → low → medium → high → xhigh → max
```

A model exposes only the levels it supports; `thinkingLevelMap` can remove a level (map it to `null`) or rename how it reaches the provider. Cycling follows that supported set. A model without reasoning support has only `off`. Unsupported levels round up to a supported level, or use the highest supported level if none is higher.

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

## Connect local models

PiG integrates with the llama.cpp router: the `/llama` command manages it and `/model` selects one of its models. For Ollama, LM Studio, vLLM, SGLang and other compatible servers, use `models.json` as shown below.

## Configure models.json

`~/.pig/agent/models.json` adds providers and models, or changes built-in ones. The top level is `{ "providers": { "<provider>": { ... } } }`. Errors in the file name its path; a missing file is fine and PiG leaves an invalid file unchanged.

Provider fields, all optional: `name`, `baseUrl`, `apiKey`, `api`, `oauth`, `headers`, `compat`, `authHeader`, `models` and `modelOverrides`. `apiKey` and header values accept `$NAME` or `${NAME}`, a literal, or a leading `!command`.

Each `models[]` entry needs an `id`; it adds a model or replaces the model with that ID on the provider. A custom model needs an `api` on itself or on its provider, or PiG reports `no "api" specified. Set at provider or model level.` and ignores the entry. Other fields:

| Field | Meaning |
|---|---|
| `name` | Label shown in the selector. |
| `reasoning` | `true` when the model supports thinking levels. |
| `thinkingLevelMap` | Maps each PiG thinking level to a provider value; `null` removes the level. |
| `input` | Input types, `["text"]` or `["text", "image"]`. |
| `inputLimits` | Image limits, `inputLimits.images` with `maxPerRequest` and `resize`. |
| `cost` | Rates per million tokens: `input`, `output`, `cacheRead`, `cacheWrite`. |
| `promptCache` | Cache lifetime in seconds for the `short` and `long` tiers. |
| `contextWindow` | Context size in tokens. Must be a positive number, or the entry is rejected (`invalid contextWindow`). PiG compacts against it. |
| `maxTokens` | Maximum output tokens per response. Must be positive. |
| `samplingParams`, `samplingParamsByThinkingLevel` | Sampling defaults, see below. |
| `headers` | Extra HTTP headers for this model. |
| `compat` | Provider compatibility switches. |

`modelOverrides` changes an existing built-in or extension model without replacing the provider's model list. It is keyed by model ID and takes the same fields except `id`. Only the fields you set change; unknown IDs are ignored.

```json
{
  "providers": {
    "openai": {
      "modelOverrides": {
        "gpt-5.5": { "contextWindow": 200000, "maxTokens": 32000 }
      }
    },
    "ollama": {
      "baseUrl": "http://localhost:11434/v1",
      "api": "openai-completions",
      "apiKey": "ollama",
      "models": [
        {
          "id": "qwen2.5-coder:7b",
          "name": "Qwen 2.5 Coder 7B",
          "contextWindow": 32768,
          "maxTokens": 8192
        }
      ]
    }
  }
}
```

The first provider gives the built-in `openai/gpt-5.5` a 200000-token window and a 32000-token output cap; everything else about it stays as shipped. The second adds a local model. To change compaction for one model instead, see `compaction.modelOverrides` in [compaction](compaction.md).

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

## Provider cost tiers

An extension's registered model can include `cost.tiers`. Each tier supplies `inputTokensAbove` and the `input`, `output`, `cacheRead` and `cacheWrite` rates per million tokens. PiG keeps these thresholds and rates through Provider registration and model lookup, and the cost calculation picks the tier from total input usage.

## Adding a model

New built-in models go into `ai/models_generated.go` and are committed in source. Extensions cannot add new built-in models, but they can register a new **provider** whose model catalog is independent (see `providers.md` and `extension-api.md`).

## Troubleshooting

- **A model does not appear.** Its provider needs usable authentication. A custom model loads from `models.json` but stays out of `/model` until PiG can resolve credentials. For llama.cpp, only models the router has loaded appear.
- **Authentication works in one shell only.** The key probably came from an environment variable, which must be present in the process that starts pig, not stored in `auth.json`.
- **Sign-in opens a browser on a remote machine.** Use the provider's headless flow when it has one; some let you paste the final redirect URL or code back into pig. See [providers](providers.md).
- **A compatible endpoint rejects requests.** Check its `api` type and `compat` settings in `models.json`; the server must support the request fields PiG sends.

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `defaultModel` contains `github-copilot/gpt-6-sol`, but a new session starts on another model | The saved default expects separate provider and model fields. | Set `"defaultProvider": "github-copilot"` and `"defaultModel": "gpt-6-sol"`. |
| `model gpt-5.5 routed to openai but I selected copilot` | Bare ID passed to `BuildModel()` | Always pass `provider/id`. |
| `GPT-5.5 does not support thinking` after a binary swap | Stale `pig` on `PATH` | Reinstall or rebuild PiG, run `hash -r`, and restart the TUI. |
| Thinking cycle has no effect | Active model has `Reasoning: false` | Switch to a reasoning model. |
| Model selector empty after `pig login` | Credentials wrote to wrong root | Check `PIG_HOME` vs `~/.pig`. |
