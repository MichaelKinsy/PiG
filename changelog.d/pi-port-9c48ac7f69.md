### Fixed

- The grep tool passes `--no-config` to ripgrep, as Pi does at `8c4e8738`, so a user's `RIPGREP_CONFIG_PATH` file cannot change the tool's contract (literal, glob, limit). <!-- fix: 645bcf8a88 -->
- Recovery of a truncated assistant attempt (retry and overflow/length compaction) finds the tool results that answered the attempt's calls in the agent transcript and omits them with it, so a resumed session no longer persists orphaned tool results that providers reject (Pi `02726086`, #11227). <!-- fix: 645bcf8a88 -->
- Classifier models take images (Pi `7f225f25`): `models.classify()` accepts `images` for TypeSafe's Jev and Cloudflare's Clef models, and provider-level `compat` in `models.json` now also applies to classifier models, with the System One fields `imageInput` and `sendImageOptInHeader`. A model without image input rejects images with a clear error. <!-- fix: a99ac58407 -->
- The catalog tolerates the `free` field Pi v1.4.0's radius data carries (`recursive/flint`), keeping it in the model record as Pi does. <!-- fix: 6d31edae7c -->
- The upstream pin moves to Pi v1.4.0. <!-- fix: 9c48ac7f69 -->
