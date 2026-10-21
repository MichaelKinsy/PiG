# Release reliability

## Read first

- `../docs/project/CONTEXT.md`
- `../docs/project/RELEASING.md`
- `../docs/project/PROVENANCE.md`
- `./SECURITY.md`

## Contract

Stock PiG artifacts and PiG Standard Piglet release artifacts are distinct. Each artifact identifies one source commit, target, toolchain, and composition. Published bytes equal tested and attested bytes. Publication requires explicit approval.

## Failure modes

| Failure | Detection | Required result |
|---|---|---|
| The source archive omits intended source | Compare `git archive HEAD` with the reviewed manifest | Match exactly |
| An artifact differs from tested bytes | Recompute its digest before publication | Stop publication |
| Stock activates Standard behavior | Run Stock without selected Resources | Keep Stock product-neutral |
| An archive crosses its extraction root | Test traversal, links, devices, and overwrite | Reject the archive |
| An update replaces a valid binary with incomplete data | Truncate and corrupt downloads | Verify before replacement |
| Public evidence exposes private context | Scan paths, domains, credentials, logs, and embedded assets | Publish no private value |
| A target is advertised without native proof | Install and smoke the release artifact on that target | Advertise only passed targets |
| One composition reuses another composition's evidence | Compare archive, SBOM, provenance, and signature identities | Keep evidence separate |

## Evidence

Build from `git archive HEAD` in an empty workspace. Test each composition and target through its public install and execution path. Produce separate checksums, SBOMs, provenance, signatures, and smoke logs. Record toolchain and source identity. After an external release backup and restore procedure is approved, test restoration before publication. Do not commit, tag, change visibility, or publish without explicit approval.

## Pull request and merge queue CI

`CI result` and `Security result` are the required status checks on `main` (ruleset `Default`, id 23613133; `.github/required-checks.txt`). Each is the aggregate job of its workflow, and each workflow runs on `pull_request` and `merge_group`, so a queue entry reports both. `test/ci-images/merge_queue_test.go` fails when a required check does not run on `merge_group`, because the queue waits on a check that no run reports until its timeout.

`automation/ci/ci-tiers.txt` assigns every CI job to a tier and `automation/ci/ci-plan.sh` turns it into the job matrices of `ci.yml`. The full run is every row. It runs on `merge_group`, on the weekly schedule, on `workflow_dispatch`, and on a pull request while the repository variable `PIG_PR_CI_LIGHT` is not `true`. The light run is the `pr` rows. It runs on a pull request only while `PIG_PR_CI_LIGHT` is `true`, which is set once the merge queue is required, so no check is skipped before a change lands on `main`. A check keeps its name in both runs. A docs-only change skips the product jobs in both, classified on the pull request's diff and on the queue entry's diff. Pushes to `main` do not run `ci.yml`: the queue lands the commit it tested, and the weekly run keeps `main`'s health current. To run the full set on a pull request branch before queueing it, dispatch CI on the branch.

| Check | Tier | Min | What stays out of the light run |
|---|---|---|---|
| Changed paths, Project documents, DCO, CI result, Linux verification, Prune superseded caches | always | 5 | |
| Linux / preflight | always | 20 | Build, and vet natively, for both Windows architectures and for Android, and `go fix` |
| Linux / test-fast-sharded | pr | 31 | |
| Linux / test-fast | pr | 26 | |
| Linux / test-cli-1 | pr | 15 | |
| Linux / test-cli-2 | pr | 12 | |
| Linux / test-subprocess | pr | 12 | |
| Linux / parity-1 | pr | 12 | One of the two hermetic parity hash shards |
| Linux / build | pr | 10 | `lint-changed` replaces the repository-wide `lint` |
| Linux / checks | pr | 9 | `module-size` and its `go install` proof |
| Linux / contracts | pr | 8 | |
| Windows native-1 | pr | 17 | |
| macOS native | pr | 13 | |
| Android build | pr | 5 | |
| Node loader / ubuntu-24.04, windows-latest, macos-15 | pr | 9 | |
| Linux / parity-2 | queue | 12 | The other hermetic parity hash shard |
| Linux / portmap-1, portmap-2, portmap-3 | queue | 44 | |
| Linux / test-conformance | queue | 18 | Conformance of every SDK |
| Linux / toolchains | queue | 7 | Race, integration, SDK and extension corpus gates |
| Node extensions | queue | 11 | |
| Durable Cloudflare host | queue | 25 | |
| Windows cli-2 cli-5, cli-1 cli-3, cli-4 cli-7, cli-6 cli-8 | queue | 96 | |
| Windows native-2, native-3, native-4 | queue | 56 | |
| Windows extension-host-1, extension-host-2, extension-host-3 | queue | 35 | |
| Windows extension-conformance-1, extension-conformance-2 | queue | 38 | |
| macOS cli-1 extension-host-3 packages, cli-2 cli-8 extension-host-2, cli-4 cli-7 extension-host-1, cli-3 cli-5 cli-6 | queue | 71 | |

Minutes are runner minutes, each job rounded up to the minute, from run 39510928254 (a full pull-request run). The full run is 617 minutes in 45 jobs and the light run 204 minutes in 21 jobs, 33% of it. At the standard billing multipliers (Windows 2, macOS 10) they are 1646 and 369 minutes; standard runners are not billed in a public repository, so the cost that matters is the share of the account's concurrent job slots. The light run is shorter than the table's 204: `lint-changed` and the `checks` shard without `module-size` replace longer gates. A change costs `k` light runs and one queue run, `204k + 617` minutes, where it cost `617k` minutes as `k` full pull-request runs, so the queue pays off from the second push to a pull request, and a queue entry that fails costs another full run.

Merge queue settings, in the `Default` ruleset: add the `merge_queue` rule with `merge_method` `SQUASH` (the ruleset's pull request rule allows only squash merges), `grouping_strategy` `ALLGREEN`, `check_response_timeout_minutes` 120, `max_entries_to_build` 2, `max_entries_to_merge` 3, `min_entries_to_merge` 1 and `min_entries_to_merge_wait_minutes` 5. Then set the repository variable `PIG_PR_CI_LIGHT` to `true`. Delete the variable to return pull requests to the full run. GitHub offers merge queues only in repositories owned by an organization; `MichaelKinsy/PiG` is owned by a user account, so the rule can be added only after the repository moves to an organization, and until then `PIG_PR_CI_LIGHT` stays unset.

Secret scanning in the queue runs TruffleHog over the commits the queue added. `gitleaks-action` supports only `push`, `pull_request`, `workflow_dispatch` and `schedule` and fails the job on `merge_group`, so that step is skipped there; the pull request run has scanned the same commits.
