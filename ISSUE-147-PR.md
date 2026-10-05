Fixes #147

`ModelRuntime.Login` returned "Unknown provider" for built-in providers and for models.json providers, because it logged in through the native collection, which holds only native registrations. `GetProvider` resolves the composed provider, built-ins included. Reported by @ShoichiTect.

- `Login` now logs in the same composed provider `GetProvider` returns (Pi `model-runtime.ts:817-829`, `models.ts:761-775`). Same-provider serialization, `CredentialSynchronizationError`, and D40 extension-owned stores are unchanged.
- `Login` accepts optional `ai.LoginOptions` (variadic) and forwards them to the provider's login, so embedders can pass `GetDeviceID`. This pairs with #146 (#151).
- `ai.Models.LoginProvider` is the new shared step. `Models.Login` looks up the registered provider and calls it.
- Tests (`coding/model_runtime_login_test.go`, red before the fix) cover a built-in, a models.json and a native provider, plus the unknown-provider and unsupported-method errors, `LoginOptions` forwarding, and built-in same-provider serialization. They use a dummy key and in-memory credential store.

Checks: `go vet` (also `GOOS=windows`), `-race -count=3` on the touched coding and internal/codingagent tests, `make interface-go-drift`. Tests that need the Pi node oracle (`ai` provider-login/auth-storage-lock, `internal/codingagent` lock contention) fail here for a missing `extensions/sdk-ts/node_modules`, with or without this change.
