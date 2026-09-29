### Added

- `coding.CreateModelRuntimeOptions.Credentials` supplies the `ai.CredentialStore` a ModelRuntime uses for authentication instead of `auth.json`, as `ModelRuntime.create({ credentials })` does in Pi. When it is set, no `auth.json` is opened; `Services.Credentials()` returns the store in effect and `Services.Auth()` is nil. `ModelRegistry.SetCredentialStore` accepts any credential store.
