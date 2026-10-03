# Pin move to upstream 0.99.1

Branch `pin-move`, worktree outside the integration checkout, based on the integration line plus family 6F-ts. Goal: the pinned upstream version is 0.99.1 everywhere, so the version guards, oracles, comparator and generated ledgers agree, and every family branch is judged against 0.99.1.

## Done

- 6F-ts merged with the 6D contract commit. The auto-merge conflict in `coding/extension/mcp_servers.go` kept the family 8a copy. `coding/mcpext` now uses the API's `mcp_servers_change` event (`extension.McpServersChangeEvent`) instead of its own copy. Build and `go vet ./...` are clean.
- `UpstreamVersion` 0.99.1, `UpstreamCommit` d86654abb8862e201933517d6f1fce9f88dd117f (`internal/coding/pigversion/pigversion.go`). PiG stays 0.3.0 until the release step (`make set-version`).
- `automation/gen/mirror-upstream.sh` no longer requires `image-models.generated.js` (upstream deleted it). The mirror `.upstream/v0.99.1` is materialized with provenance and `.upstream/current` points at it.
- `extensions/sdk-ts` is locked to 0.99.1 (`npm ci --min-release-age=0` installs it).

## Open, in dependency order

1. **Catalog generators (blocking, file-disjoint, a lane can take it).** Upstream 0.99.1 splits the catalog: `dist/providers/<provider>.models.js` imports `dist/providers/data/<provider>.json` and flattens it into chat, image and classifier models (`flatten*ModelCatalog` in `dist/model-catalog.js`). `make model-catalogs` (`automation/gen/generate-model-catalogs.sh`) still reads `models.generated.js` as one file and `image-models.generated.js`, which is gone. Rebuild `cmd/gen-models` and `cmd/gen-image-models` (or merge them) to read the per-provider JSON, emit `ai/models_generated.go` and `ai/image_models_generated.go`, and carry the classifier models. Unblocks the data-dependent tests listed under "Blocked" in the main progress file (Kimi K3, glm-5p3, gpt-6.1-sol, Sonnet 5.5, `model-data-validation`, `image-model-data`, `together-models`, `fireworks-*`, `supports-xhigh`, `max-thinking`).
2. Extractor fix: `test/parity/interface-extractor/src/behavior-input-inventory.mjs` fails with `duplicate renderer id render:packages/coding-agent/src/extensions/mcp/ui.ts#McpManagerView.render` (owner-approved fix).
3. `TRACKED_PACKAGES` in `test/parity/interface-extractor/src/inventory.mjs` gains `mcp` and `codemode` (D-H).
4. PORT_MAP: add the rows for the added source files and remove the rows whose upstream file no longer exists (`make port-map-drift` lists both).
5. Regenerate centrally, in this order: `make interface-proposals`, `make test-inventory-generate`, `make behavior-input-inventory`, `go run ./test/parity/cmd/upstreamdelta -generate -from 0.87.1 -to 0.99.1`, `make interface-delta`, `make known-gaps`, `make custom-factory-ledger`, `make help-text`, `make knowledge-graph`, `make coverage`. New versioned ledgers (`*-v0.99.1.json`, `upstream-sync/v0.99.1.toml`) replace the 0.87.1 ones; no ledger row for a portable test goes to `deferred`.
6. Oracles: regenerate every remaining 0.87.1 oracle from the real 0.99.1 packages on Node 24.19.0 (recipe in the family 2 detail of the main progress file), starting with the faux, test-faux, backpressure (`rpc-usage-probe.mjs`) and RPC33 sets. The probes' version guards already assert 0.99.1 for the family 2 set.
7. Divergence guard: `structuredOutputMaxBytes`, `wheelMaxAutoLines` and the 13 `coding/mcpext` hits pass against the 0.99.1 mirror. The two baseline entries for `ai/oauth_anthropic.go` and `ai/oauth_openai_codex.go` need the family 3 merge.
8. Comparator: `make parity-deps` checks `pi --version` against `UpstreamVersion`; with the sdk-ts lock at 0.99.1 the paired scenarios compare against real 0.99.1.
9. Joint run: `go test -race` per package (not `make test`: the darwin kqueue hang in `internal/experimental`), then `make parity-fast`.

## Docs sweep (needs an owner)

`python3 automation/ci/check-public-claims.py` reports 147 lines that still say "Pi 0.87.1" while the pin is 0.99.1: README (badge and status line), `docs/site/docs/*`, `internal/pigdocs/content/*`, `CHANGELOG.md`, `docs/parity/*`, `docs/findings/*`, `docs/performance/*`, `docs/extension-api-parity.md`, `docs/additive-features.md` and `docs/extension-sdk-surface.md`. The pin statements (README badge, `docs/site/docs/index.md` table) become 0.99.1. Each behavior statement ("PiG ships the same provider set as ...", "Pi ... also emits ...") has to be checked against upstream 0.99.1 and then reworded: a blind rename would claim a version the statement was never checked against. The docs mirrors for the new upstream docs (`docs/mcp.md`, `docs/virtual-models.md`, 19 changed docs) belong to the same sweep (`internal/pigdocs`, docs-drift gate).

## Status log

- Docs sweep merged (`35b93ecc1`): `check-public-claims` passes at the 0.99.1 pin and `check-scratch-paths` passes.
- Waiting for review verdicts: catalog-gen (item 1), ledgers-oracles (items 2 and 3).
- Landing step (after those merge here): merge `porter/pi-0.99.1` into `porter/pin-move`, fix conflicts, run `make ci-drift`, then the joint run, then merge `porter/pin-move` into the integration line.
