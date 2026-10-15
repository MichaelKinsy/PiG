# Custom providers

A PiG extension can register or override model providers. Use this mechanism when an OpenAI-compatible `models.json` entry is not sufficient or when the provider needs custom authentication, refresh, or request behavior.

Provider registration is an extension capability. Keep provider-specific code outside Stock PiG.

## Choose the smallest mechanism

| Need | Use |
|---|---|
| Add an OpenAI-compatible endpoint and static models | `~/.pig/agent/models.json` |
| Add provider metadata or OAuth | Provider extension |
| Add custom request or stream behavior | Provider extension |
| Select the provider for one agent application | Piglet `model` preference plus the provider extension |
| Supply product credentials or endpoints | External product configuration, not portable Piglet source |

See [Models](/docs/latest/models) before you create a provider extension.

## Worked example: a local model with `models.json`

Most local servers (Ollama, LM Studio, vLLM, llama.cpp's server) speak the OpenAI chat API. You add one with a JSON file and no code. Save this as `~/.pig/agent/models.json`, with your server's address and the model name it serves:

```json
{
  "providers": {
    "ollama": {
      "baseUrl": "http://localhost:11434/v1",
      "api": "openai-completions",
      "apiKey": "ollama",
      "models": [
        { "id": "llama3.2", "name": "Llama 3.2 (local)", "contextWindow": 128000, "maxTokens": 4096 }
      ]
    }
  }
}
```

| Field | Meaning |
|---|---|
| `providers.<name>` | The provider name. You use it in `--model <name>/<id>`. |
| `baseUrl` | The server address of the API. An OpenAI-compatible server usually ends the address with a `v1` path part. |
| `api` | The protocol. `openai-completions` is the OpenAI chat API. A custom model needs an `api` on the model or on its provider, unless it replaces a built-in model of that provider. Without one, PiG reports `no "api" specified. Set at provider or model level.` and ignores the provider's entry, as Pi does. PiG 0.4.x defaulted it. |
| `apiKey` | The key to send. A local server usually ignores it, but PiG requires some value: without `apiKey`, the provider's models are left out of the list with no error, and PiG says `No models available`. |
| `models[].id` | The model's name on the server. Spell it as the server lists it. |
| `models[].contextWindow`, `maxTokens` | How much the model can hold, and the longest reply to ask for. They drive the footer percentage and compaction. |

Check that PiG sees the model. The table lists every model PiG can use, yours included:

```bash
pig --list-models
```

```text
provider  model     context  max-out  thinking  images
ollama    llama3.2  128K     4.1K     no        no
```

Run it:

```bash
pig --model ollama/llama3.2 -p "Say hello"
```

PiG sends the request to `baseUrl`. If the server is not running, the command prints this and exits 1:

```text
Connection error.
```

Start the server and run it again. A model chosen with `/model` or `Ctrl+L` lists under its provider name. `Ctrl+S` in the selector makes it the default.

A mistake in the file is reported with the file's path, and the rest of PiG still starts. This is the message for a file that is not valid JSON:

```text
Warning: errors loading models.json:
Failed to parse models.json: Expected property name or '}' in JSON at position 16 (line 2 column 1)

File: /home/you/.pig/agent/models.json
No models available. Use /login to log into a provider via OAuth or API key. ...
```

Edit the file and start PiG again. For a model that does not exist on the server, the server's own error comes back, so check the model name first. [Models](/docs/latest/models) covers sampling options for this file and [Providers](/docs/latest/providers) lists the built-in providers.

## Register a provider

Create a Go extension:

```bash
pig extension init ./corporate-ai --lang go
```

Register provider configuration from its factory:

```go
package corporateai

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
    ext := sdk.New("corporate-ai")
    ext.RegisterProvider("corporate-ai", sdk.ProviderConfig{
        "name":    "Corporate AI",
        "baseUrl": "https://ai.example.com/v1",
        "apiKey":  "$CORPORATE_AI_API_KEY",
        "api":     "openai-completions",
    })
    return ext
}
```

Use environment interpolation for a secret. Do not place a literal credential in extension source or Piglet YAML.

Validate the extension against the binary that will run it.

## Validate registration

```bash
pig install ./corporate-ai --validate-only --json
```

List models after selecting the extension:

```bash
pig -e ./corporate-ai --list-models
```

Use a Piglet when the provider belongs to a named application:

```yaml
name: corporate-review
extensions:
  - name: corporate-ai
    origins: [local:./extensions/corporate-ai]
model:
  provider: corporate-ai
  name: review-model
```

## OAuth providers

An extension can register an OAuth provider through the same provider declaration. The language SDK exposes callbacks for:

- browser authorization;
- device codes;
- progress messages;
- text prompts;
- option selection;
- manual code input;
- token refresh;
- API-key derivation;
- optional extension-owned credential storage.

PiG inspects registration before a Session so the provider can appear in:

```bash
pig login --list
pig login corporate-ai
```

Registration inspection constructs the extension but does not dispatch Session, tool, command, event, shortcut, or renderer handlers.

Duplicate provider IDs fail and identify both owners. Login starts only the extension that owns the selected provider.

In interactive `/login`, an option-selection prompt opens a selector in the login dialog. Cancelling the selector cancels the login.

A provider object logs in with the methods its `auth` declares, as in Pi. Node extensions pass it to Pi's `registerProvider(provider)` overload. The Go SDK takes it with `ext.RegisterNativeProvider(*sdk.Provider)`, and in-process Go code with `extension.API.RegisterNativeProvider`. `/login` lists an account sign-in when `auth.oauth` is declared, labelled with its `loginLabel`. The API-key login runs the provider's own `auth.apiKey.login` and its prompts. A provider without its own API-key login gets the generic API-key prompt. A provider object can declare no auth method at all, as Pi's `ProviderAuth` allows.

An `auth.oauth` login receives the app's login options as its second argument, as in Pi: `func(sdk.AuthInteraction, sdk.LoginOptions) (*sdk.Credential, error)` in Go, `login(interaction, options)` in Python and `Fn(AuthInteraction, LoginOptions)` in Rust. `LoginOptions.GetDeviceID` is nil in Go when the app supplies no device ID. An `info` event of the login shows in the login dialog with its message and links, and a `secret` prompt follows the **Mask secret input** setting.

A Go provider's credential is an ordered `*sdk.Credential`. Build one with `sdk.NewCredential("type", "api_key", "key", key)`, read it with `Get`, `String` and `Keys`, and refresh it with `Clone` and `Set`. `auth.json` stores the credential's keys in the order the provider gave them, as Pi does. `pig extension upgrade` rewrites a Go provider written for the earlier `map[string]any` credential and login signature where it can, and names the change where it cannot (see [Extensions](/docs/latest/extensions)).

`sdk.ProviderStreamOptions` carries `Signal`, `Values`, `OnPayload`, `OnResponse` and `OnProviderStreamEvent`. It has no `TransformHeaders`: Pi's model runtime applies `transformHeaders` itself and never passes it to a provider's stream.

## Credential ownership

Use PiG's normal credential store when the provider does not declare its own store. Use an extension-owned store only when the provider contract requires it.

An extension-owned store must:

- protect file permissions;
- avoid logging tokens;
- report whether credentials are present;
- delete credentials through the matching logout path;
- surface errors instead of returning false success.

Environment variables take precedence when the provider contract defines that behavior.

## Dynamic provider state

Provider registrations belong to the live extension generation. PiG removes them when the extension shuts down or a replacement fails to register.

A successful atomic reload replaces provider state with the new validated extension set. A failed replacement keeps the previous working set.

## Per-request HTTP transport

Node extensions can pass Pi's `fetch` option to model requests. PiG calls that function in the originating extension process. It sends the request URL, method, headers, and serialized body unchanged and reads the response body on demand. Cancellation, response-read errors, and transport errors remain on that request. Closing the response or shutting down the extension releases its reader. The native Go equivalent is `ai.StreamOptions.Fetch`.

This option applies to HTTP, not WebSocket connections. Google Generative AI and Google Vertex reject custom fetch functions, as in Pi. For Cloudflare binding routes, use the binding auth sentinel and explicit `Authorization: null` and `x-api-key: null` headers so placeholder credentials do not override gateway-managed credentials.

## Connection failure

A provider extension can hold live state even when no tool call is active. PiG keeps heartbeat active while the connection owns that state.

If the connection fails, PiG cancels pending calls and removes state owned by the failed generation. It does not replay a provider request automatically.

## Fused providers

A compatible Go provider extension can be fused into a Piglet Binary. Fusion does not change registration or credential semantics.

PiG Standard requires all selected extensions to fuse. A provider added to Standard must therefore be a compatible Go factory and pass source and fused lifecycle tests.

## Security

A provider extension can receive prompts, model responses, headers, and credentials. Review it as security-sensitive code.

Do not:

- hardcode credentials;
- send PiG or user data to an undeclared endpoint;
- hide certificate failures;
- retry non-idempotent requests without a defined policy;
- retain tokens after logout;
- claim an upstream provider identity for a different service.

## Pi reference

Pi exposes its provider API to in-process TypeScript extensions. PiG maps the same behavior through its extension SDKs and host contract.

See [upstream Pi's custom provider documentation](https://pi.dev/docs/latest/custom-provider) for the reference implementation.

## Related documentation

- [Models](/docs/latest/models)
- [Providers](/docs/latest/providers)
- [Extensions](/docs/latest/extensions)
- [Piglets](/docs/latest/piglets)
