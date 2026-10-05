# Providers

PiG speaks to inference providers through the provider registry shared with its Pi compatibility model. Each built-in provider has a Go implementation under `ai/` that handles authentication, request shaping, and stream parsing. Extensions can register additional providers through the public extension API. See [Extensions](/docs/latest/extensions).

Most hosted providers accept an API key, and some also accept a browser or device sign-in through OAuth. Amazon Bedrock and Google Vertex AI can also use ambient cloud credentials. Run `/login` or `pig login --list` to see the sign-in methods for each provider.

## Built-in providers

Raw Provider-object access from extensions has documented 0.3.x gaps (D78, owner decision 2026-09-28). Registered-native methods cross the SDK bridge, but Go, Rust and Python cannot yet retrieve every builtin/composed raw Provider object. Foreign registered-configuration data is a snapshot, not a live alias of the author's object; callable handles do not synchronize arbitrary property writes. Same-process Node roots, children, functions and receivers must retain Pi's behavior. This limit does not change normal model selection or approve incorrect authentication, refresh, cancellation or registration cleanup.

PiG ships the same built-in chat providers and classifier models as Pi 1.0.2. `typesafe` has only classifier models, and `cloudflare-workers-ai`, `opencode`, `openrouter` and `vercel-ai-gateway` list them beside their chat models. The provider key is the first part of a `provider/model` spec. The wire column lists the APIs that the provider's built-in chat models use.

| Provider key | Name | Wire | Credential |
|---|---|---|---|
| `amazon-bedrock` | Amazon Bedrock | `bedrock-converse-stream` | AWS credential chain or `AWS_BEARER_TOKEN_BEDROCK`. See [Amazon Bedrock](#amazon-bedrock). |
| `ant-ling` | Ant Ling | `openai-completions` | `ANT_LING_API_KEY` |
| `anthropic` | Anthropic | `anthropic-messages` | `ANTHROPIC_API_KEY`, `ANTHROPIC_OAUTH_TOKEN`, `ANTHROPIC_AUTH_TOKEN`, workload identity federation, or OAuth. See [Anthropic workload identity federation](#anthropic-workload-identity-federation). |
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

### Anthropic workload identity federation

When no key, stored credential, `ANTHROPIC_AUTH_TOKEN` or authorization header is available, `anthropic` can authenticate with workload identity federation. Set `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID` and `ANTHROPIC_IDENTITY_TOKEN_FILE`. `ANTHROPIC_SERVICE_ACCOUNT_ID` and `ANTHROPIC_WORKSPACE_ID` are optional.

PiG reads the identity token (a JWT) from the file on every exchange, so a rotated token is picked up. It exchanges the token for an access token with a `jwt-bearer` grant at `<base URL>/v1/oauth/token` and sends the access token as `Authorization: Bearer`. PiG keeps the access token and refreshes it before it expires. After a 401 the next request exchanges again. The token endpoint must use `https`; `http` is accepted only for `localhost`, `127.0.0.1` and `::1`. An identity token over 16 KiB is rejected before the request. An exchange failure is the request's error, and no message request is sent. Error bodies keep only the `error`, `error_description` and `error_uri` fields.

Federation applies to the `anthropic` provider only. Other `anthropic-messages` providers still require a key or an authorization header.

`github-copilot` reads only `COPILOT_GITHUB_TOKEN`. A general `GITHUB_TOKEN` is not a Copilot credential.

`PI_CACHE_RETENTION` sets the prompt cache retention that PiG passes to the provider.

Mistral requests include `prompt_cache_key` and an automatic `x-affinity` header when the request has a session ID and cache retention is not `none`. Explicit affinity headers remain unchanged, including an empty header. Replayed assistant messages use `prefix: false`; they are completed turns, not completion prefixes.

Google Gemini and Vertex requests encode the system instruction as user-role content, matching Pi's Google SDK. They omit the tool-calling mode when no explicit choice or strict tool schema requires it. Explicit `none` and `any` choices take precedence over strict `VALIDATED` mode.

## Authentication

Provider authentication can require several replies. Bedrock supports a bearer token, an AWS profile, or the existing AWS credential chain. Vertex supports an API key, Application Default Credentials, or a service-account file, with project and location prompts. Cloudflare requests an account ID and, for AI Gateway, a gateway ID after the key. Provider-scoped settings are stored in the credential's `env` object, not in the chat transcript.

API keys are stored as entered. Leading and trailing spaces are preserved. An explicitly empty stored Cloudflare key does not fall back to `CLOUDFLARE_API_KEY`. An empty Vertex key does not select `GOOGLE_CLOUD_API_KEY`; Vertex still checks its cloud credentials. Use `/logout` to remove a stored credential and return to environment-only authentication. Provider searches include authentication method names. A provider without credentials is labelled `not configured`. A credential for another authentication type is labelled `API key configured`, `subscription configured`, or `account configured` in the provider list. OAuth sign-ins backed by a subscription are labelled `subscription`; other OAuth sign-ins, such as Radius and OpenRouter, are labelled `account`.

PiG stores credentials in `~/.pig/agent/auth.json`. Agent startup creates a missing file containing `{}` with owner-only permissions (mode `0600` on POSIX; an owner-only DACL on Windows, D68). Startup leaves existing contents and permissions unchanged. Stored credentials take precedence over environment fallback; a runtime API key can override them for the current process.

`auth.json` can contain API keys and OAuth tokens. Keep it private and do not commit it.

Interactive login prompts marked as secret use PiG's `maskSecretInput` setting (default `true`). **Mask secret input** in `/settings` shows dots, a character count and the last four characters while typing, then retains only the masked preview after submission. Inputs shorter than five characters show no suffix. Set `maskSecretInput` to `false` to restore Pi 1.0.2's plain-text behavior. This configurable feature is recorded as divergence D80. Ordinary text and manual-code prompts remain visible. Credentials still belong in `auth.json` or the provider's credential store; the setting protects dialog and authentication-diagnostic output, not credential storage.

### Interactive login and logout

Run `/login <provider>` with a provider ID or display name to configure that provider. Argument completion shows each provider's supported methods. A provider with one method opens that flow directly. A provider with several methods opens its own method selector. An unmatched argument opens a searchable provider list.

Run `/login` without an argument to choose an authentication method first. The last option, **Sign in with Radius**, starts the Radius sign-in directly and shows its status. Press Escape in its provider list to return to the method selector. Press Escape at a sign-in method prompt, such as Anthropic's, Radius's, Amazon Bedrock's or Google Vertex AI's, or close a provider's setup note, to return to the menu the login was started from. Keep answering the prompts inside the login dialog until it reports completion. Escape in a key or text prompt aborts the login and leaves existing credentials unchanged. Pi reports this cancellation as `Failed to save API key for <Name>: This operation was aborted`.

Run `/logout` to remove a stored API-key or OAuth credential. The list excludes environment-only credentials. A successful logout also removes the provider's `--api-key` override for the current run. If stored-credential deletion fails or is cancelled, the override remains available. Logout does not change environment variables or `models.json`. If no credentials are stored, PiG reports that there is nothing to remove instead of opening an empty picker.

A matching login option reports its credential source, such as `stored` or an environment variable. The logout list marks the stored credentials it can remove as `configured`. These labels describe different states.

OpenAI-compatible HTTP errors retain Pi's status and provider error body. Completion errors use the status directly. Responses errors include the provider's API-error prefix.

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

A stored API-key credential can include an `env` object. Its values take precedence over the process environment for that provider. Model Runtime streaming and extension model-auth lookups carry these values to the provider request:

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

On Windows, PiG passes browser login URLs directly to the Windows URL handler without a command shell. URL parameters and shell metacharacters remain part of the URL.

Device-code login shows the verification URL, user code, and waiting status without opening a browser. Open the displayed link yourself. Browser authorization URL events still open the default browser.

Built-in OAuth targets are `anthropic`, `github-copilot`, `kimi-coding`, `meta`, `openai-codex`, `openrouter`, `radius`, and `xai`. Each provider owns its flow. For example, GitHub Copilot uses device authorization, while callback-based providers can open a localhost callback server. Tokens are persisted to `auth.json` unless the provider owns another store, and supported providers refresh them when required.

Anthropic login asks for **Browser login (default)** or **Copy code login (headless)**. Browser login listens for the callback on the local machine and also accepts the pasted redirect URL. Copy code login works when the browser runs on another machine: sign in through the printed URL, then paste the code Anthropic shows (`code#state`). Escape in the login dialog reports `Failed to login to Anthropic: This operation was aborted`.

Anthropic login cancels and waits for its contextual manual-code prompt when authorization succeeds or fails. `PI_OAUTH_CALLBACK_HOST` selects its callback listener host; the authorization request retains the localhost redirect URI.

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

Claude Opus 5 uses adaptive thinking and supports native `xhigh` effort on Bedrock. PiG recognizes the model ID or the display name of an application inference profile and adds prompt-cache points for supported Claude models. For GovCloud model IDs, GovCloud ARNs, or a configured `us-gov-` region, PiG omits `thinking.display`, which the GovCloud schema rejects.

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

Radius is a gateway that speaks Pi's own message protocol, `pi-messages`: PiG posts the conversation to `<baseUrl>/messages` and reads the reply as a stream of Pi events. `/login` → **Sign in with Radius** (or **Sign in with an account** → **Radius**) offers a browser sign-in (a callback on `127.0.0.1:1456`) or a device code for signing in from another machine. You can also paste a key with **Sign in with an API key** or set `RADIUS_API_KEY`. After a Radius sign-in, `/login` offers to configure the Radius MCP server in the global `mcp.json` with `"auth": { "provider": "radius" }`; answering **Yes** writes the entry and runs `/reload`. Nothing is asked when a global server for the Radius MCP URL already uses the Radius login.

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

Anthropic Messages requests retain the selected model's reasoning capability, thinking-level map, and output limit. A newly supplied model can use `compat.forceAdaptiveThinking` without an entry in the built-in catalog.

Extensions can add inference providers at register time. PiG validates incoming model configuration before replacing registered model data. Set `api` when registering `streamSimple`. For a new model, supply `api` at the provider or model level. A rejected registration leaves the existing registry entry and native Provider owner intact.

The host retains provider configurations until a registry binds. Binding applies them in order and reports each failed entry with its owning extension path. Later registration and removal take effect immediately. The host owns lifecycle: providers registered by an extension are unregistered automatically on shutdown, reload failure, or quarantine fission. When a reload replaces an extension whose new register declares the same provider name, the registration is preserved across the swap so streaming completions are not interrupted.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Missing bearer or basic authentication` | No API key in env, no token in `auth.json` | `pig login <provider>` or export the env var. |
| `pig auth check --provider google` prints `not_ready` | Only `GOOGLE_API_KEY` is set | Set `GEMINI_API_KEY`. |
| `Bad credentials` (Copilot 401) | OAuth token expired or revoked | `pig login github-copilot`. |
| Model selector shows nothing | No providers have valid auth | Login or set an env var; check `/login`. |
| Cycling lands on the wrong model | Bare ID in a custom helper | Always pass `provider/model`; see [Models](/docs/latest/models). |
