### Changed

- The in-process extension `ProviderOAuth.RefreshToken` callback receives the refresh's `context.Context`, as Pi's `refreshToken(credentials, signal)` receives its `AbortSignal`, so a cancelled refresh reaches the extension.
- `env.SshArgumentsWith(target, strictHostKeys, knownHostsFile)` is the exported three-parameter form of Pi's `sshArguments`.
- The settings selector's `MermaidRenderingMode` and `OutputPad` values are typed (`"off" | "final" | "streaming"`, `0 | 1`) in `SettingsConfig`, `SettingsCallbacks` and the settings manager.
- Summarization (compaction, branch summary, bug report) takes Pi's `retry` and `callbacks` as `ai.RetryPolicy` and `ai.RetryCallbacks`; the private `compaction.RetryPolicy`, `RetryCallbacks` and `RetryOptions` are removed.
