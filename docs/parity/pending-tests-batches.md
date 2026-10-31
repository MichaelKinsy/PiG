# Upstream test porting batches

## Dispatch contract

Baseline: `c475b56d028166a429b7fe35476520550780a471`, Pi 1.4.0+6c384aa3. This document partitions every baseline pending and partial test file exactly once: **0 pending + 0 partial = 0 files**, across **0 batches**. Counts come from `upstream-tests-v1.4.0+6c384aa3.json`; they are case sites, not expanded `it.each` matrices or a count of cases still missing in partial files.

Hot-path batches come first. Target Go packages combine existing evidence locations with the owning production area; they are navigation targets, not proof of closure. New harness execution/runtime packages need their separate API and production callers, not substitutions from the older agent loop. Read the mapping rationale to enumerate missing cases before changing a partial entry.

Assign one owner per upstream test file. Keep closed files in their original batches so no baseline obligation disappears. Dispatch only rows still pending or partial. Test-file assignments do not authorize simultaneous edits to shared Go packages, SDK protocols, or generated ledgers; the lead coordinates those collisions and serializes integration and shared inventory updates.

Current mapping SHA-256: `e630d5d2964f98e867efa05a8af3ab9fb99bf92c777941460f3577b038eb8b65`. Current dispositions below are a generated snapshot; the JSON mapping remains authoritative.

Regenerate after integration:

```bash
python3 automation/gen/pending-test-batches.py --baseline-commit c475b56d028166a429b7fe35476520550780a471 --version 1.4.0+6c384aa3
```

## Batch index

| Batch | Priority | Area | Files | Upstream case sites | Still pending/partial |
|---|---|---|---:|---:|---:|
