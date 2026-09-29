# Provider registration cost tiers

Pi0.87.1 `packages/coding-agent/test/extensions-runner.test.ts:50–79,1173–1195` registers `instant-provider`/`instant-model` with threshold272000 and tier rates2/3/0.2/2.5. The shared `extension.ProviderModelCost` carrier omitted tiers, so JSON decoding discarded them before registry composition.

`TestProviderRegistrationCostTiersRoundTrip` and `TestNodePostBindProviderRegistrationRetainsCostTiers` are both source-red on an isolated `d5dba7d51` snapshot. The latter starts a real Node extension, registers after bind, checks the complete original tier in ModelRuntime, unregisters, and checks absence. Adding the existing `ai.CostTier` slice to the carrier preserves the data without changing a composer. The llama adapter uses explicit base-rate fields because its four-field cost type no longer has the carrier's complete struct shape.

The isolated minimal change passes three race repetitions, root vet, Windows touched-package vet and touched-package lint. Initial lint was blocked by another golangci-lint process; the serial-runner option waits for that lock without weakening checks. Canonical63 passes three Pi/PiG pairs using the same model input. Its startup registration assertion complements the post-bind Host test; it does not claim full runner queue/bind closure.

The provider owner's retained lane evidence includes `cost-tier-checkpoint-{red,green,vet,windows-vet,lint-serial,parity}.log` and `cost-tier-checkpoint-results.json`. The earlier Host test draft had incorrect Go CostTier field names and did not compile; that draft is not red evidence. The isolated snapshot failure is the behavioral red. No registration/composer implementation, Context guard, timeout, comparator or approval is changed by this correction.
