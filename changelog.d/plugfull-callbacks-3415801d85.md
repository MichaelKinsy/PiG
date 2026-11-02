### Added

- `chord/nextnode/plugins`: the app's Go functions and JSON values reach its plugins as host packages (`GoPackage`), and the Node host calls the app's `telemetry` adapter, `principalKey` and `revokedBy` (`RuntimeLoad`/`PluginsLoad`), with `NodeHost.Revoke` and `NodeHost.ExtractTelemetry`.
