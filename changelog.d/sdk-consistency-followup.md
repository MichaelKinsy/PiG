### Fixed

- Preserve empty, null and provider-owned OAuth metadata in writable auth storage through a persisted rewrite and reopening, as Pi's auth storage does. A provider field that shares a name with an API-key or convenience property, such as `accountId: false`, no longer makes `auth.json` unreadable.
- Let typed Go and Rust tool definitions send Pi's explicit `constrainedSampling: false` instead of only omitting it. Go uses `sdk.DisabledConstrainedSampling{}` through the new `ToolConstrainedSampling` union; Rust uses `ToolConstrainedSampling::Disabled`.
