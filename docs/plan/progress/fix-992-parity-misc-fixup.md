# fix-992-parity-misc-fixup

Base: `staging/rev-fix-992-parity-misc` (`ba886dcbd`). Source: joint-run-6 aggregate r1.

## Outcome: no production or test change was required

Both reported failures do not reproduce on the base. The investigation below is the evidence.

### 1. `TestModelRegistryDynamicProvidersUpstream/getProviderDisplayName_...`

- Pi: `getProviderDisplayName(provider)` is `this.runtime.getProvider(provider)?.name ?? provider` (`.upstream/v0.99.2/packages/coding-agent/src/core/model-registry.ts:176-178`). The composed provider name is `extension?.name ?? config?.name ?? base?.name ?? extension?.oauth?.name ?? providerId` (`.upstream/v0.99.2/packages/coding-agent/src/core/provider-composer.ts:588`).
- Base `ModelRegistry.GetProviderDisplayName` (`internal/codingagent/model_registry.go`) returns the composed provider's name, then the radius provider name, then the registered name, then the registered OAuth name, then the catalog name or the id. `1f71fc68e` still removed `builtInProviderDisplayNames`, and the registered and OAuth names survive because `72dc60336` (in the base) keeps an empty native provider name for providers outside the catalog.
- `go test -count=1 ./coding/` passes, and `go test -race -count=5 -run TestModelRegistryDynamicProvidersUpstream ./coding/` passes.
- Order probe (not committed): registering the built-in id `anthropic` with an OAuth-only extension named `Corp SSO` returns `Anthropic`, as Pi's `base?.name` precedes `oauth?.name`.

### 2. parity `interactive-rendering/27-compact-read-labels`

- Pi renders `[skill] <dir>` collapsed and `read <path>` expanded deterministically (`.upstream/v0.99.2/packages/coding-agent/src/core/tools/renderers/read.ts:67-111,151-160`). Fixture and PiG are consistent with 0.99.2.
- The scenario passes 25/25 pairs (`-pig-parity.runs=25`) on the base under a host load average of 65, and 3/3 on a merge of the aggregate tip `328306114` (scratch archive).
- The retained aggregate artifact (`agg-992/test/parity/artifacts/27-compact-read-labels/20261001T074323Z/context.json`) records `capture-pane: exit status 1 (server pid exited ... no server running)` for the Pi run: the tmux server vanished, so the Pi capture was empty. That is the host-sharing harness flake routed to `fix-992-parity-harness`, not a Pi-vs-PiG difference. The older artifact (`20261001T062403Z`) differs only by Pi's tmux `extended-keys` warning, an environment line.

## Verification

- `go test -count=1 ./coding/` ok; `go test ./internal/codingagent/` ok (with the Pi 0.99.2 `extensions/sdk-ts/node_modules` oracle linked).
- Parity (hermetic, real Pi 0.99.2): `selectors/10-login-subscription-providers` pass; `interactive-rendering/27-compact-read-labels` pass.
- `selectors/01-config-empty` fails on this base because the scenario still asserts `No resources found`, which Pi 0.99.2 does not print (built-in extensions are listed). Another lane's change on the aggregate already updates that scenario; not in this slice.

## Review (rev-fix-992-parity-misc-fixup)

- The joint-run-6 aggregate r1 (`agg-992`, `8b681bc29`) merged `fix-992-parity-misc` at `43f98326f` (second parent of `66b9ffaed`). That snapshot predates `72dc60336`, so the native base for an unknown provider still took `ai.ProviderDisplayName(id)` (the id) and hid the OAuth name. The r1 failure is a stale lane snapshot, not an interaction with another lane: merging `rev-fix-992-parity-misc` into `agg-992` r1 makes the subtest pass.
- `TestModelRegistryDynamicProvidersUpstream` passes on `porter/pi-0.99.1` `091fed811` (contains the merge `36c543070`) and on `agg-992-r2` `328306114`.
- Mutation: restoring `Name: ai.ProviderDisplayName(id)` in `RegisterProviderInput` reproduces the exact aggregate failure `OAuth name="oauth-provider"`.
- `interactive-rendering/27-compact-read-labels` passes 5/5 pairs on `porter/pi-0.99.1` against Pi 0.99.2.
