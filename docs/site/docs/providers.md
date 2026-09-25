# Providers

PiG speaks to inference providers through the provider registry shared with its Pi compatibility model. Each built-in provider has a Go implementation under `ai/` that handles authentication, request shaping, and stream parsing. Extensions can register additional providers through the public extension API. See [Extensions](/docs/latest/extensions).

Most hosted providers accept an API key, and some also accept a browser or device sign-in through OAuth. Amazon Bedrock and Google Vertex AI can also use ambient cloud credentials. Run `/login` or `pig login --list` to see the sign-in methods for each provider.

## Built-in providers

PiG ships the same built-in provider set as Pi 0.87.1. The provider key is the first part of a `provider/model` spec. The wire column lists the APIs that the provider's built-in models use.

| Provider key | Name | Wire | Credential |
|---|---|---|---|
| `amazon-bedrock` | Amazon Bedrock | `bedrock-converse-stream` | AWS credential chain or `AWS_BEARER_TOKEN_BEDROCK`. See [Amazon Bedrock](#amazon-bedrock). |
| `ant-ling` | Ant Ling | `openai-completions` | `ANT_LING_API_KEY` |
| `anthropic` | Anthropic | `anthropic-messages` | `ANTHROPIC_API_KEY`, `ANTHROPIC_OAUTH_TOKEN`, `ANTHROPIC_AUTH_TOKEN`, or OAuth |
| `azure-openai-responses` | Azure OpenAI Responses | `azure-openai-responses` | `AZURE_OPENAI_API_KEY` plus an endpoint. See [Azure OpenAI](#azure-openai). |
| `baseten` | Baseten | `openai-completions` | `BASETEN_API_KEY` |
| `cerebras` | Cerebras | `openai-completions` | `CEREBRAS_API_KEY` |
| `cloudflare-ai-gateway` | Cloudflare AI Gateway | `anthropic-messages`, `openai-completions`, `openai-responses` | `CLOUDFLARE_API_KEY`, `CLOUDFLARE_ACCOUNT_ID`, and `CLOUDFLARE_GATEWAY_ID` |
| `cloudflare-workers-ai` | Cloudflare Workers AI | `openai-completions` | `CLOUDFLARE_API_KEY` and `CLOUDFLARE_ACCOUNT_ID` |
| `deepseek` | DeepSeek | `openai-completions` | `DEEPSEEK_API_KEY` |
| `fireworks` | Fireworks | `anthropic-messages`, `openai-completions` | `FIREWORKS_API_KEY` |
| `github-copilot` | GitHub Copilot | `anthropic-messages`, `openai-completions`, `openai-responses` | OAuth via `pig login github-copilot`, or `COPILOT_GITHUB_TOKEN` |
| `google` | Google Gemini | `google-generative-ai` | `GEMINI_API_KEY` |
| `google-vertex` | Google Vertex AI | `google-vertex` | `GOOGLE_CLOUD_API_KEY` or Application Default Credentials. See [Google Vertex AI](#google-vertex-ai). |
| `groq` | Groq | `openai-completions` | `GROQ_API_KEY` |
| `huggingface` | Hugging Face | `openai-completions` | `HF_TOKEN` |
| `kimi-coding` | Kimi For Coding | `anthropic-messages` | `KIMI_API_KEY` or OAuth |
| `meta` | Meta | `openai-responses` | `META_API_KEY` or OAuth |
| `minimax` | MiniMax | `anthropic-messages` | `MINIMAX_API_KEY` |
| `minimax-cn` | MiniMax (China) | `anthropic-messages` | `MINIMAX_CN_API_KEY` |
| `mistral` | Mistral | `mistral-conversations` | `MISTRAL_API_KEY` |
| `moonshotai` | Moonshot AI | `openai-completions` | `MOONSHOT_API_KEY` |
| `moonshotai-cn` | Moonshot AI (China) | `openai-completions` | `MOONSHOT_API_KEY` |
| `nvidia` | NVIDIA NIM | `openai-completions` | `NVIDIA_API_KEY` |
| `openai` | OpenAI | `openai-responses` | `OPENAI_API_KEY` |
| `openai-codex` | OpenAI Codex (ChatGPT subscription) | `openai-codex-responses` | OAuth via `pig login openai-codex` |
| `opencode` | OpenCode Zen | `anthropic-messages`, `google-generative-ai`, `openai-completions`, `openai-responses` | `OPENCODE_API_KEY` |
| `opencode-go` | OpenCode Go | `anthropic-messages`, `openai-completions`, `openai-responses` | `OPENCODE_API_KEY` |
| `openrouter` | OpenRouter | `anthropic-messages`, `openai-completions` | `OPENROUTER_API_KEY` or OAuth |
| `qwen-token-plan` | Qwen Token Plan | `openai-completions` | `QWEN_TOKEN_PLAN_API_KEY` |
| `qwen-token-plan-cn` | Qwen Token Plan (China) | `openai-completions` | `QWEN_TOKEN_PLAN_CN_API_KEY` |
| `qwen-token-plan-individual` | Qwen Token Plan (Individual) | `openai-completions` | `QWEN_TOKEN_PLAN_API_KEY` |
| `radius` | Radius | `pi-messages` | `RADIUS_API_KEY` or OAuth. See [Radius](#radius). |
| `together` | Together AI | `openai-completions` | `TOGETHER_API_KEY` |
| `vercel-ai-gateway` | Vercel AI Gateway | `anthropic-messages` | `AI_GATEWAY_API_KEY` |
| `xai` | xAI | `openai-responses` | `XAI_API_KEY` or OAuth |
| `xiaomi` | Xiaomi MiMo | `openai-completions` | `XIAOMI_API_KEY` |
| `xiaomi-token-plan-ams` | Xiaomi MiMo Token Plan (Amsterdam) | `openai-completions` | `XIAOMI_TOKEN_PLAN_AMS_API_KEY` |
| `xiaomi-token-plan-cn` | Xiaomi MiMo Token Plan (China) | `openai-completions` | `XIAOMI_TOKEN_PLAN_CN_API_KEY` |
| `xiaomi-token-plan-sgp` | Xiaomi MiMo Token Plan (Singapore) | `openai-completions` | `XIAOMI_TOKEN_PLAN_SGP_API_KEY` |
| `zai` | ZAI Coding Plan (Global) | `openai-completions` | `ZAI_API_KEY` |
| `zai-coding-cn` | ZAI Coding Plan (China) | `openai-completions` | `ZAI_CODING_CN_API_KEY` |

`pig --list-models` shows only the providers that have a credential. `pig --list-models <search>` filters that list.

To add an endpoint that is not in this table, such as a local Ollama, LM Studio or vLLM server, declare it in `~/.pig/agent/models.json`. See [Custom providers](/docs/latest/custom-provider).

## Use an API key from the environment

Set the provider's variable before you start PiG:

```bash
export GEMINI_API_KEY=...
pig --model google/gemini-2.5-flash
```

Each provider in the table above reads the variable in its credential column. Use `GEMINI_API_KEY` for `google`. PiG does not treat `GOOGLE_API_KEY` as a `google` credential: `pig auth check`, `/login` and `--list-models` ignore it, as Pi does.

`anthropic` reads three variables. `ANTHROPIC_AUTH_TOKEN` is sent as an `Authorization: Bearer` token and takes precedence over the other two. `ANTHROPIC_OAUTH_TOKEN` is used as an API key and takes precedence over `ANTHROPIC_API_KEY`. Subscription tokens (`sk-ant-oat`) are sent with the Claude Code identity, and subscription auth shows a warning at session start.

`github-copilot` reads only `COPILOT_GITHUB_TOKEN`. A general `GITHUB_TOKEN` is not a Copilot credential.

`PI_CACHE_RETENTION` sets the prompt cache retention that PiG passes to the provider.

## Authentication

PiG stores credentials in `~/.pig/agent/auth.json`. The file is created on demand by `pig login` and is never read at module init. Environment variables (`OPENAI_API_KEY`, etc.) always take precedence over stored credentials at request time, matching upstream behavior - this lets per-shell or per-project keys override the global file.

`auth.json` can contain API keys and OAuth tokens. Keep it private and do not commit it.

### Load an API key from a command

To use a secret manager without writing the key to disk, set a stored key to a command that starts with `!`:

```json
{
  "anthropic": {
    "type": "api_key",
    "key": "!security find-generic-password -ws 'anthropic'"
  }
}
```

PiG runs the command when the key is first needed and uses its standard output as the key.

### Provider settings in a credential

A stored API-key credential can include an `env` object. Its values take precedence over the process environment for that provider:

```json
{
  "cloudflare-workers-ai": {
    "type": "api_key",
    "key": "...",
    "env": {
      "CLOUDFLARE_ACCOUNT_ID": "account-id"
    }
  }
}
```

### OAuth providers

Built-in OAuth targets are `anthropic`, `github-copilot`, `kimi-coding`, `meta`, `openai-codex`, `openrouter`, `radius`, and `xai`. Each provider owns its flow. For example, GitHub Copilot uses device authorization, while callback-based providers can open a localhost callback server. Tokens are persisted to `auth.json` unless the provider owns another store, and supported providers refresh them when required.

A failed Copilot refresh reports an explicit reauthentication instruction:

```
github-copilot: refresh failed - run 'pig login' to re-authenticate
```

### `pig login` shapes

```
pig login                 # interactive picker
pig login <provider>      # specific provider
pig login --list          # list the targets without reading credentials
pig logout <provider>     # remove credentials for one provider
```

`pig logout` does not unset environment variables or revoke the credential at the provider.

### Credential commands

`pig auth` resolves credentials the way a model request does and writes them for external clients. Each command needs `--provider <provider>`, `--model <model>`, or both.

```
pig auth check --provider openai --json      # ready / not_ready / invalid, exit 0 / 1 / 2
pig auth print-api-key --provider openai     # API key on stdout
pig auth print-bearer-token --provider openai-codex --min-expiry 1h
```

Credential-printing commands write secrets to stdout. `auth check` prints a credential only with `--credentials`.

Use `pig auth check` to confirm that PiG sees a credential before you start a session.

An OAuth login can wait for device authorization or a browser callback. This wait belongs to the provider flow. PiG does not start a model Session while `pig login` runs.

## Cloud providers

The providers below need more than one setting, or can use credentials from their cloud platform.

### Azure OpenAI

Set an API key and either a base URL or a resource name:

```bash
export AZURE_OPENAI_API_KEY=...
export AZURE_OPENAI_BASE_URL=https://your-resource.ai.azure.com
# Or:
export AZURE_OPENAI_RESOURCE_NAME=your-resource
```

| Variable | Effect |
|---|---|
| `AZURE_OPENAI_API_KEY` | API key. |
| `AZURE_OPENAI_BASE_URL` | Azure OpenAI or Foundry endpoint. `.openai.azure.com`, `.cognitiveservices.azure.com`, and `.ai.azure.com` hosts are normalized to `/openai/v1`. |
| `AZURE_OPENAI_RESOURCE_NAME` | Alternative to `AZURE_OPENAI_BASE_URL`; builds `https://<resource>.openai.azure.com/openai/v1`. |
| `AZURE_OPENAI_API_VERSION` | API version. Default `v1`. |
| `AZURE_OPENAI_DEPLOYMENT_NAME_MAP` | Optional comma-separated `model=deployment` map for Azure deployments. |

### Amazon Bedrock

Bedrock uses a bearer token or the standard AWS SDK credential chain:

```bash
# Named profile
export AWS_PROFILE=your-profile

# IAM keys
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...   # for temporary credentials

# Bedrock bearer token
export AWS_BEARER_TOKEN_BEDROCK=...

# Region, when the profile or SDK configuration does not supply one
export AWS_REGION=us-west-2    # AWS_DEFAULT_REGION also works
```

ECS task credentials and IRSA work through the standard `AWS_CONTAINER_CREDENTIALS_*` and `AWS_WEB_IDENTITY_TOKEN_FILE` variables.

### Cloudflare AI Gateway

The gateway needs a token, an account ID, and a gateway ID:

```bash
export CLOUDFLARE_API_KEY=...
export CLOUDFLARE_ACCOUNT_ID=...
export CLOUDFLARE_GATEWAY_ID=...
```

The account and gateway IDs can come from the process environment or from the credential's `env` object in `auth.json`.

### Cloudflare Workers AI

Workers AI needs a token and an account ID:

```bash
export CLOUDFLARE_API_KEY=...
export CLOUDFLARE_ACCOUNT_ID=...
```

### Google Vertex AI

Use a Google Cloud API key:

```bash
export GOOGLE_CLOUD_API_KEY=...
```

To use Application Default Credentials instead, set a project and a location, then sign in:

```bash
export GOOGLE_CLOUD_PROJECT=your-project   # GCLOUD_PROJECT also works
export GOOGLE_CLOUD_LOCATION=us-central1
gcloud auth application-default login
```

To use a service-account key file, set `GOOGLE_APPLICATION_CREDENTIALS` with the project and location.

### Radius

Radius is a gateway that speaks Pi's own message protocol, `pi-messages`: PiG posts the conversation to `<baseUrl>/messages` and reads the reply as a stream of Pi events. `/login` → **Sign in with an account** → **Radius** offers a browser sign-in (a callback on `127.0.0.1:1456`) or a device code for signing in from another machine. You can also paste a key with **Sign in with an API key** or set `RADIUS_API_KEY`.

PiG ships Radius's published model list. With credentials configured, PiG fetches the gateway's current list from `<gateway>/v1/config` in the background when interactive or RPC mode starts and after you sign in with `/login`, and caches it in `~/.pig/agent/models-store.json`. Print mode and `--list-models` use the cached list. `PI_OFFLINE` (any value) or `PIG_OFFLINE` (`1`, `true` or `yes`) turns the fetch off. Without Radius credentials, PiG contacts the gateway only while you sign in.

To use another Radius gateway, add a provider with `"oauth": "radius"` to `models.json`. `baseUrl` is required; PiG drops a trailing `v1` path segment to find the gateway. The published model list applies only to the default gateway.

```json
{
  "providers": {
    "radius-dev": { "name": "Radius (dev)", "baseUrl": "http://localhost:8788", "oauth": "radius" }
  }
}
```

Any backend that implements `pi-messages` works as a `models.json` provider with `"api": "pi-messages"`, a `baseUrl` and an `apiKey`.

## Provider resolution

When you select a model through `--model`, `/model`, `Ctrl+P`, or `setModel()`,
PiG parses `provider/model`. Always use provider-qualified model specs from
extensions and helpers; bare IDs default to `openai` and can route incorrectly.

For Copilot models the model ID itself may contain a slash (e.g. `github-copilot/openai/gpt-5.5`); pig's `generatedModelSpec` helper preserves trailing slashes when rebuilding specs from generated model objects.

## Provider extensions

Extensions can add inference providers at register time. The host treats the `config` payload as opaque. The host owns lifecycle: providers registered by an extension are unregistered automatically on shutdown, reload failure, or quarantine fission. When a reload replaces an extension whose new register declares the same provider name, the registration is preserved across the swap so streaming completions are not interrupted.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Missing bearer or basic authentication` | No API key in env, no token in `auth.json` | `pig login <provider>` or export the env var. |
| `pig auth check --provider google` prints `not_ready` | Only `GOOGLE_API_KEY` is set | Set `GEMINI_API_KEY`. |
| `Bad credentials` (Copilot 401) | OAuth token expired or revoked | `pig login github-copilot`. |
| Model selector shows nothing | No providers have valid auth | Login or set an env var; check `/login`. |
| Cycling lands on the wrong model | Bare ID in a custom helper | Always pass `provider/model`; see [Models](/docs/latest/models). |
