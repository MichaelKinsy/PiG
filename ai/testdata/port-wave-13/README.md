# Pi 1.3.0 Anthropic E2E case snapshot

`anthropic_e2e_cases.json` lists the Anthropic Messages models of the published pinned pi-ai catalog and the probes the upstream acceptance tests select from them.

SHA-256: `50077c7c6dd8963032e1cb80322405d04c32a5dfdc017e06e7300590295366f8`.

The snapshot contains the 405 Anthropic Messages model identities of the pinned pi-ai catalog (Pi 1.3.0+482c1381), 10 configured probes, and 9 forced-eager probes. The selector preserves the priority and JavaScript `localeCompare` rules in `packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:34-85` and `packages/ai/test/anthropic-long-cache-retention-e2e.test.ts:22-70`. The test checks the complete catalog denominator independently against Pig's catalog. It does not replace a remote response with fixture data.

The owning regeneration command is `node test/parity/testdata/generate-anthropic-e2e-cases.mjs > ai/testdata/port-wave-13/anthropic_e2e_cases.json` after `make model-catalogs` installs the pinned package. Applied to the 0.87.1 data, the same selector reproduces the previous reviewed snapshot byte for byte in content.

Select the `live` build tag for the provider acceptance tests. The Anthropic thinking-disable and Bedrock thinking-payload cases that abort in `OnPayload` run in the default suite without credentials. Supply provider environment keys in an isolated home. Copilot uses `PIG_LIVE_COPILOT_TOKEN`, which contains the resolved token normally returned by the upstream OAuth helper. No test reads the worker's auth files. Missing credentials fail the selected live case. The selected catalog routes require Anthropic, Cloudflare AI Gateway (including account/gateway endpoint configuration), Fireworks, GitHub Copilot, Kimi Coding, MiniMax, MiniMax CN, OpenCode, OpenRouter, and Vercel AI Gateway access.
