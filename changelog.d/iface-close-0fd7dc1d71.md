### Added

- Go API `durable`: `ToolCreateTask` and `harness.HookEntry` are the typed forms of Pi's `ToolExecutionApi.createTask` and `HookApi.entry(token, id)`, which Go methods cannot take as generics. `harness.NewRegistryReader` is Pi's `registryReader`: it reads a Chord registry value, composed as a registry snapshot, as a `RegistryReader` that wakes the Harness scheduler on each composition.
