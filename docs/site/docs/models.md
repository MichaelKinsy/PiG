# Models

A model in PiG is identified by a **provider-qualified spec**: `provider/modelID`. Model-selection options such as `--model`, `/model`, `Ctrl+P` cycling, and the extension `setModel()` call accept provider-qualified specs. Scoped model entries in settings also use that form. The saved default is an exception: set `defaultProvider` to the provider and `defaultModel` to the model ID without the provider prefix. A provider-qualified `defaultModel` does not select that model at startup.

`--model` and `/model` resolve a bare model ID across all providers, as Pi does. The Go function `coding.BuildModel` is different: it reads a spec without a `/` as `openai/<id>`. Always pass `provider/id` to `BuildModel`.

## Select a model

| Mechanism | Effect |
|---|---|
| `pig --model openai/gpt-5.5` | Override for this process. |
| `/model` slash command | Open the selector. Enter selects for this session. Ctrl+S selects and saves the default. |
| `Ctrl+P` / `Shift+Ctrl+P` | Cycle next/previous within scoped models. |
| `/scoped-models` | Edit the cycle list interactively. |
| `--models a,b,c` | Set scope for this run only (does not persist). |
| `setModel(spec)` (extension API) | Programmatic switch. The Go SDK's `Context.SetModel` returns `(applied, error)`. |

The selector lists models from providers with configured authentication. It uses `GEMINI_API_KEY` for Google Gemini, not `GOOGLE_API_KEY`. Providers without configured authentication are omitted.

An extension can add models that choose a physical model for each request. See [Virtual models](virtual-models.md).

## Connect local models

PiG integrates with the llama.cpp router. The router discovers GGUF files and loads models on demand. The `/llama` command manages the router, and `/model` selects one of its models. See [llama.cpp](llama-cpp.md).

For Ollama, LM Studio, vLLM, SGLang, and other compatible servers, [configure a compatible endpoint](#configure-a-compatible-endpoint) in `models.json`.

## Configure a compatible endpoint

Use `~/.pig/agent/models.json` when an endpoint speaks an API that PiG already supports. This includes most Ollama, LM Studio, vLLM, SGLang, and proxy deployments:

```json
{
  "providers": {
    "ollama": {
      "baseUrl": "http://localhost:11434/v1",
      "api": "openai-completions",
      "apiKey": "ollama",
      "models": [
        { "id": "qwen2.5-coder:7b" }
      ]
    }
  }
}
```

The dummy key makes the model available; Ollama ignores it. For an authenticated endpoint, `apiKey` and header values can use `$NAME` or `${NAME}` environment interpolation, a literal value, or a leading `!command`. [Custom providers](custom-provider.md) has a worked example.

A custom model needs an `api` on the model or on its provider, unless it replaces a built-in model of that provider. Without one, PiG reports `Provider "<provider>": Provider <provider>, model <model>: no "api" specified. Set at provider or model level.` and ignores that provider's `models.json` entry, as Pi does. PiG 0.4.x defaulted the `api`. A model whose `contextWindow` or `maxTokens` is zero or negative is rejected the same way (`Provider <provider>, model <model>: invalid contextWindow`).

A `models` entry adds or replaces a model with the same ID on that provider. Use `modelOverrides` to change the metadata of an existing built-in or extension-provided model without replacing the provider's model list. Unknown override IDs are ignored.

### Describe model input and caching

Use `inputLimits.images.resize` to control how PiG encodes new image attachments, `read` results, and tool-result images before it stores them in the conversation history:

```json
{
  "id": "vision-model",
  "input": ["text", "image"],
  "inputLimits": {
    "images": {
      "resize": {
        "maxWidth": 1568,
        "maxHeight": 1568,
        "maxBytes": 524288,
        "jpegQuality": 75
      }
    }
  }
}
```

`maxBytes` limits the base64-encoded payload. Omitted resize fields use defaults of 2000 by 2000 pixels, 4.5 MiB encoded, and JPEG quality 80. Images are encoded once; changing models does not rewrite historical images.

`inputLimits.images.maxPerRequest` limits the images in one request. When a request carries more, PiG replaces the oldest images with the text `[image omitted: this model accepts at most N images per request]` and keeps the newest N. The stored session is unchanged. Pi reads the limit and ignores it, so the provider rejects the request (D137, Pi #10162). Catalog models with a limit, such as Anthropic's, are affected without configuration. Once a run passes the limit, each request that adds an image moves the omitted set, so the provider's prompt cache misses from the oldest kept image onward.

Use `promptCache` to declare the provider's cache lifetime in seconds for the `short` or `long` retention tier:

```json
{ "id": "claude-sonnet-5", "promptCache": { "short": 300, "long": 3600 } }
```

A model without a lifetime for the active tier is not eligible for cache warming. A `modelOverrides` entry can set `inputLimits` or `promptCache` for a built-in or extension model. See [cache warming](settings.md#cache-warming).

### Configure sampling by thinking level

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

## Scoped models

Scoped models are the list `Ctrl+P` cycles through. They are stored in settings as provider-qualified IDs; PiG accepts legacy bare IDs and rewrites them on next save. Cycling preserves both `provider` and `id`, including an `id` that contains a slash, such as `openrouter/openai/gpt-5.5`.

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

A model is reasoning-capable when its catalog entry sets `reasoning: true`; `thinkingLevelMap` controls how each level is wired into the request.

## Model metadata

Extensions read the active model with `ctx.model` (Node) or `Context.GetModelInfo()` (Go SDK, returns `(*ModelInfo, error)`). The Go SDK's `ModelInfo` carries `ID`, `Name`, `Provider`, `ContextWindow`, `MaxOutputTokens`, `Reasoning`, `InputLimits` and the per-million-token costs `InputCostPer1M`, `OutputCostPer1M`, `CacheReadCostPer1M` and `CacheWriteCostPer1M`.

## Use classifier models

Classifier models do not chat. They answer typed questions about JSON state: pick one of several choices, answer yes or no, or give a score, each with probabilities. The built-in catalog includes TypeSafe's Jev model from these providers, Cloudflare's Clef and Clef Flash models from Workers AI, and OpenAI's GPT-6 Luna through the [Decisions API](https://developers.openai.com/api/docs/guides/decisions):

| Provider | Model IDs | Authentication |
|---|---|---|
| `typesafe` | `jev-latest` | `TYPESAFE_API_KEY` |
| `openrouter` | `typesafe/jev-1.13`, `~typesafe/jev-latest` | `OPENROUTER_API_KEY` or `/login` |
| `cloudflare-workers-ai` | `typesafe/jev`, `@cf/cloudflare/clef`, `@cf/cloudflare/clef-flash` | `CLOUDFLARE_API_KEY` and `CLOUDFLARE_ACCOUNT_ID` |
| `vercel-ai-gateway` | `typesafe-ai/jev` | `AI_GATEWAY_API_KEY` |
| `opencode` | `jev-1.13`, `jev-1.13-free` | `OPENCODE_API_KEY` |
| `openai` | `gpt-6-luna` | `OPENAI_API_KEY` |

Chat models on a [llama.cpp router](llama-cpp.md#classification) are also listed as classifier models.

OpenAI's Decisions API needs an API key. Sign in with ChatGPT credentials do not work with it, so `gpt-6-luna` is not listed as available while `openai` is logged in through `/login`, even when `OPENAI_API_KEY` is set; log out of `openai` to use the key. GPT-6 Luna also judges images passed in `images` (see [Codemode](codemode.md#classify)); other classifier models return an error for them. The API rejects inputs above 922K tokens, but requests that run longer than about five seconds, currently above roughly 600K input tokens, fail with a gateway timeout.

Classifier models do not appear in `/model`. The model reaches them through the [`codemode`](codemode.md) tool, which is off unless an MCP server turned it on. Enable it with `"defaultTools": ["+codemode"]` in [settings](settings.md#tools). Scripts list classifier models with `models.getAvailableOfType("classifier")` and call `models.classify(model, { state, questions })`. [Codemode](codemode.md#classify) describes the question and answer types.

PiG adds the usage of a script's classifier calls to the `codemode` tool result, so it counts toward the session cost. The cost uses the model's catalog price; a model without one, such as TypeSafe's direct `jev-latest`, reports tokens at no cost.

Extensions call classifiers through `ctx.modelRegistry.classify()`, without codemode. [Virtual models](virtual-models.md) can use them to route requests.

## Use image models

Image models generate images from a prompt and optional input images. PiG lists OpenRouter's image models, such as `google/gemini-2.5-flash-image` and `black-forest-labs/flux.2-pro`, under the `openrouter` provider; they use the same `OPENROUTER_API_KEY` or `/login` credential as its chat models. OpenRouter serves some image models, such as `openai/gpt-image-2.5-flare`, only through its Images API. When `chat/completions` refuses such a model, PiG sends the request once more to the Images API; Pi returns the refusal as an error (D128, Pi #10652).

Image models do not appear in `/model`; the model reaches them through the `codemode` tool. Scripts list them with `models.getAvailableOfType("image")` and call `models.generateImages(model, { input })`. The result's `output` holds base64 image blocks, which `image()` attaches to the `codemode` result so the model sees them:

```js
const painter = await models.getModelOfType("image", "openrouter", "google/gemini-2.5-flash-image");
const result = await models.generateImages(painter, {
  input: [{ type: "text", text: "A red fox in the snow, watercolor" }],
});
if (result.stopReason !== "stop") return result.errorMessage;
for (const block of result.output) if (block.type === "image") image(block);
```

`input` can also contain `{ type: "image", data, mimeType }` blocks to edit or use as references. PiG adds the usage of a script's image calls to the `codemode` tool result, like classifier calls. Generated images are not saved to disk. [Codemode](codemode.md#generate-images) describes the full API.

Extensions generate images through `ctx.modelRegistry.generateImages()`, without codemode.

## Add a custom provider

New built-in models are generated into `ai/models_generated.go` from Pi's catalog. Use an extension when a provider needs custom streaming, model discovery, or authentication. See [Custom providers](custom-provider.md) and [Providers](providers.md).

## Provider cost tiers

An extension's registered model can include `cost.tiers`. Each tier supplies `inputTokensAbove` and the `input`, `output`, `cacheRead`, and `cacheWrite` rates per million tokens. PiG retains these thresholds and rates through Provider registration and model lookup. The cost calculation selects the applicable tier from total input usage.

## Common errors

Model configuration read, JSON parse, and schema errors include a `File:` line that identifies the selected `models.json` path. Check that file's contents and permissions. A missing file is allowed. PiG leaves invalid files unchanged.

| Symptom | Cause | Fix |
|---|---|---|
| `defaultModel` contains `github-copilot/gpt-6-sol`, but a new session starts on another model | The saved default expects separate provider and model fields. | Set `"defaultProvider": "github-copilot"` and `"defaultModel": "gpt-6-sol"`. |
| `no "api" specified. Set at provider or model level.` | A custom model in `models.json` has no `api`. | Add `"api"` to the provider or the model. |
| A model routed to `openai` from Go code | Bare ID passed to `coding.BuildModel()` | Pass `provider/id`. |
| A custom model is missing from `/model` | Its provider has no usable credential. | Configure `apiKey` or sign in. For llama.cpp, load the model first. |
| Thinking cycle has no effect | The active model does not support reasoning. | Switch to a reasoning model. |
| Model selector empty after `/login` | Credentials were written to another configuration root. | Check `PIG_HOME` against `~/.pig`. |

See [upstream Pi's model documentation](https://pi.dev/docs/latest/models) for the reference implementation.
