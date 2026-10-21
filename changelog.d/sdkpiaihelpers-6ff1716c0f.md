### Added

- The Go, Rust and Python extension SDKs export Pi's pure pi-ai helpers: `StringEnum`, `getSupportedThinkingLevels`, `clampThinkingLevel`, `calculateCost`, `contentText`, `normalizeContext`, `getCurrentTools`, `getCurrentSystemMessage`, `getCurrentSystemPrompt` and the other transcript helpers, `isContextOverflow`, `isRecoverableLength`, `isRetryableAssistantError`, `retryDelayMs`, `getModelType`, `isModelType`, `hasApi`, `modelsAreEqual` and `envApiKeyAuth`, checked against Pi's own code at the pin. <!-- fix: 6ff1716c0f -->
- The SDKs export `getPackageDir`, `getShellConfig`, `generateDiffString`, `generateUnifiedPatch`, `convertToPng`, `resizeImage`, `copyToClipboard`, `sessionEntryToContextMessages` and `readStoredCredential`; the first six are served by the host (new host calls, and `PIG_EXT_HOST_INFO` carries the package directory and the default shell). <!-- fix: 6ff1716c0f -->
- `createAssistantMessageEventStream` exists in Rust and Python, and in all three SDKs the stream adds `durationMs` to its final message as Pi's does. <!-- fix: 6ff1716c0f -->

### Upgrading

- `ResizedImage` in the Go, Rust and Python SDKs gains `Data`/`data` and `MimeType`/`mime_type`; the Rust struct is no longer `Copy`, and a literal needs `..Default::default()`. `HostInfo` in the host's subprocess package gains `PackageDir` and `Shell`.
