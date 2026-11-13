### Added

- `PIG_TELEMETRY` overrides the `enableInstallTelemetry` setting, beside Pi's `PI_TELEMETRY`, which still works and wins when both are set. `pig` passes it on as `PI_TELEMETRY` to the Node extension runtime.

### Changed

- Messages that named Pi as the product that prints them name pig or PiG (divergence record D2, "Product name in messages"): `Session file is not a valid pig session: <path>`, the "another pig session" hints of the ChatGPT sign-in and of a git package update, the tmux `extended-keys-format` warning, the description of the "Show hardware cursor" setting, and the models.json reference page. The extensions link of the extension deprecation warning points at PiG's documentation. The JSON schemas keep Pi's text.
- `pig --help` lists `PIG_OFFLINE`, `PIG_SELF_UPDATE_COMMAND` and `PIG_TELEMETRY`. Pi's `PI_OFFLINE`, `PI_SELF_UPDATE_COMMAND` and `PI_TELEMETRY` keep working.
- Messages of `pig status`, the Piglet commands, the Piglet build and `pig extension init` spell the product PiG where they said Pig, for example `PiG status: healthy`, `PiG runtime: ...` and the `PiG:` line of a signed manifest. A script that matches `Pig status:` must match `PiG status:`.
- The default-model diagnosis says "PiG reads" where it said "Pi reads".
