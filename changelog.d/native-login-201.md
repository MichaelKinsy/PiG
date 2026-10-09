### Fixed

- `/login` offers a provider object's own login methods (Pi's `registerNativeProvider`, the `registerProvider(provider)` overload). A provider whose `auth` declares an OAuth method now lists "Sign in with an account" under its own `loginLabel`, and it stays listed after the provider's catalog refresh. Reported and diagnosed by @balcsida (#201).
- A login flow's `select` prompt opens a selector in the login dialog, as Pi's does, instead of ending the login as "Login cancelled". Cancelling the selector cancels the login and returns to the menu it was started from. This applies to provider objects and to `config.oauth` providers.
- A provider object's own `auth.apiKey.login` runs in the login dialog, as Pi's `showApiKeyLoginDialog` runs it, so a provider that asks for its base URL before the key stores both. Each answered prompt stays visible in the dialog. A provider object without its own API-key login keeps the generic API-key prompt.
- A provider object's login that sends a prompt or event type Pi does not define fails with an error that names the type, instead of being answered as a text prompt or shown as progress.
- A cancelled prompt in a `config.oauth` login now rejects with Pi's `Login cancelled` in every SDK (it was `oauth prompt cancelled`, and Node's `onSelect` resolved `undefined`), so `/login` returns to the menu instead of reporting a failed login.
- A `config.oauth` login's `onManualCodeInput` asks "Paste the authorization code", as Pi does, and a provider object's `manual_code` prompt shows its own message and placeholder.
- An extension login callback (`onPrompt`, `onSelect`, `onManualCodeInput`) whose prompt fails for a reason other than the user's cancel now reports that error to the flow. Before, every failure reached the flow as a cancel.
- A login flow's text prompt accepts an empty answer, as Pi's does, instead of failing with "<prompt> is required".
