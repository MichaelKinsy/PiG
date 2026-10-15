# Design-out audit proposal, lg-c4-coding-agent-a (parked for the lead; not the live file)

All five open coding-agent rows (a-m range) are design-outs that `test/parity/interface-closure/breakdown/proofclass.py` counts as unproven until `docs/parity/designout-audit.tsv` exists with parity, maintainability and performance columns. `designout-audit-proposal-lg-c4-coding-agent-a.tsv` here uses the same layout as the lg-c4-ai-b proposal (`id`, `basis`, `parity`, `maintainability`, `performance`). Each row's text was checked against the pinned Pi 1.1.0 source and the current Go code:

| row | basis | check |
|---|---|---|
| `AgentSessionConfig::property:extensionRunnerRef` | D113 | `agent-session.ts:296,498,3644-3645` and `sdk.ts:333` read; owner decision 2026-10-10 (design-out audit Q1 option B) |
| `CredentialSynchronizationError::property:stack` | D103 | the error's other members compared with Pi by `TestCredentialSynchronizationErrorMatchesPi` |
| `defineTool`, `defineTool::call:0` | identity function | `types.ts:676` returns its argument |
| `discoverAndLoadExtensions::call:0` | D117 | discovery compared with Pi by `TestDiscoverExtensionPathsMatchPi` (300 seeded trees) |

Audit judgments are reviewed inputs, so this is a proposal: nothing is written to `docs/parity/designout-audit.tsv`. To adopt it, append the rows (without the header) to that file with its own header. Adopting it with the lg-c4-ai-b proposal closes the five rows in coding-agent.
