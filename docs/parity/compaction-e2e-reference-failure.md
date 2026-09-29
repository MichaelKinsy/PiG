# Compaction E2E reference expectation

The case at Pi 0.87.1 `packages/coding-agent/test/agent-session-compaction.test.ts:84-107` expects `session.messages[0].role` to be `compactionSummary` at line 107. The pinned implementation instead saves a system checkpoint in `packages/coding-agent/src/core/session-manager.ts:1268-1281` and projects it before the summary at lines 461-463. The newer Session suite explicitly expects `system` followed by `compactionSummary` at `packages/coding-agent/test/suite/agent-session-compaction.test.ts:185-186`.

The lane lead approves the pinned implementation as the oracle: **upstream test expectation disagrees with the pinned implementation; PiG matches the implementation**. The Go port asserts the system checkpoint followed by the compaction summary and retains the original nonempty-summary, positive-token, and nonempty-message assertions. It does not filter system messages. This is not a divergence or a designed-out case. All five cases in the E2E file are ported.

## Reference probe

```bash
node --experimental-import-meta-resolve test/parity/testdata/session-compaction-e2e-pi.mjs
```

The probe loads unchanged installed Pi 0.87.1. It matches the original Agent system prompt, coding tools, disk-backed SessionManager and SettingsManager, minimal retention, prompt strings, and wait-for-idle calls. It substitutes only the approved faux model/auth. Its first two answers are `4` and `6`; its summary responses are nonempty. Longer ordinary responses produce the same role order:

```json
["system","compactionSummary","assistant"]
```

`reference-original-assertion-red.log` records executing the original assertion after this probe, with the installed package version checked as 0.87.1:

```text
AssertionError [ERR_ASSERTION]: Expected values to be strictly equal:
+ actual - expected

+ 'system'
- 'compactionSummary'
```

The original assertion exits 1. The approved implementation-based case passes in `coding/session_compaction_e2e_upstream_test.go`. A compiling mutation that omits manual compaction's context refresh fails the new case and the paired scenario because the second message remains a user message rather than the summary; the mutation is restored before final verification. The canonical scenario `test/parity/scenarios/compaction/15-manual-compaction-checkpoint-order.toml` compares the complete role sequence and original summary/token predicates against Pi with exact output equality and three pairs. Dynamic raw token counts are not part of the upstream assertion.
