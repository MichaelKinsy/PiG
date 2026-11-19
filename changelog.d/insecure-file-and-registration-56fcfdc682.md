### Changed

- A provider that `models.json` marks `insecure: true` and an extension registers again now answers one rule on every request route. A registration that states `insecure` wins, `true` or `false`; one that states nothing keeps the file's opt-in only while it keeps the file's endpoint, and one that names its own `baseUrl` verifies certificates at that endpoint. Before, the two routes a request takes disagreed: a request through the model runtime dropped the file's opt-in whenever the provider was registered (since 0.3.0), and a provider built straight from a model entry (`coding.BuildModel`, which the CLI's `--model`, the interactive model picker and an extension's model switch use for a provider without a native stream function) kept the file's opt-in and ignored the registration's, even an explicit `insecure: false` (since 0.2.0).

### Security

- A registration's explicit `insecure: false` now verifies certificates on every route; before, a provider built from a model entry kept the file's `insecure: true` and skipped verification. An opt-in written in `models.json` for one endpoint no longer carries over to a different endpoint an extension names.
