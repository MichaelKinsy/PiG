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

`automation/ci/ci-tiers.txt` assigns every CI job to a tier and `automation/ci/ci-plan.sh` turns it into the job matrices of `ci.yml`. The full run is every row; the light run is the `pr` rows. A check keeps its name in both runs. A docs-only change skips the product jobs in both, classified on the pull request's diff and on the queue entry's diff. The full run runs on `merge_group`, on the weekly schedule, on `workflow_dispatch`, and on a pull request unless the repository variable `PIG_PR_CI_LIGHT` selects a light mode. Pushes to `main` do not run `ci.yml`: a change lands on the tree that was tested, and the weekly run keeps `main`'s health current.

`PIG_PR_CI_LIGHT` is the switch, and no value lets a change land on a light run alone:

| `PIG_PR_CI_LIGHT` | A pull request push runs | `CI result` on that run | Where the full run happens |
|---|---|---|---|
| unset | the full run | passes when green | on the pull request |
| `label` (no merge queue) | the light run | fails with "full run required: add the ci:full label" unless this run is the full run on the pull request's current head | on the pull request, when it carries the `ci:full` label, is marked ready for review, or CI is dispatched on its branch |
| `queue` (merge queue required) | the light run | passes when green | on `merge_group` |

The flow in `label` mode: push; the light run reports `CI result` red with the message; right before merging, add the `ci:full` label (the `labeled` event starts a full run and cancels a light run in flight); merge when `CI result` is green on the head. The label stays on the pull request, so a later push runs the full set again, and removing it returns the next push to the light run, whose `CI result` fails. The full run's `CI result` also fails when the pull request's head is no longer the head the run tested (it asks GitHub when it finishes), so a run for an older head cannot report success for a newer one. A docs-only change needs no label: the light and the full run test it identically. Marking a draft ready for review starts a full run on that head only; a later push is light again. Dispatching CI on the branch (`gh workflow run ci.yml --ref <branch>`) is the third way to get a full run on the current head. [INFERENCE] When one commit carries a failed `CI result` from a light run and a green one from the full run, GitHub evaluates the latest check run of that name; that has not been observed on this repository, so check the merge box the first time.

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

Minutes are runner minutes, each job rounded up to the minute, from run 39510928254 (a full pull-request run). The full run is 617 minutes in 45 jobs and the light run 204 minutes in 21 jobs, 33% of it. At the standard billing multipliers (Windows 2, macOS 10) they are 1646 and 369 minutes; standard runners are not billed in a public repository, so the cost that matters is the share of the account's concurrent job slots. The light run is shorter than the table's 204: `lint-changed` and the `checks` shard without `module-size` replace longer gates. In both light modes a change with `k` pushes costs `204k + 617` minutes (`k` light runs and one full run: the queue's, or the `ci:full` run) where it cost `617k` as `k` full pull-request runs, so a light mode pays off from the second push, and a failed full run costs another 617.

Merge queue settings, in the `Default` ruleset, for `queue` mode: add the `merge_queue` rule with `merge_method` `SQUASH` (the ruleset's pull request rule allows only squash merges), `grouping_strategy` `ALLGREEN`, `check_response_timeout_minutes` 120, `max_entries_to_build` 2, `max_entries_to_merge` 3, `min_entries_to_merge` 1 and `min_entries_to_merge_wait_minutes` 5, then set `PIG_PR_CI_LIGHT` to `queue`. GitHub offers merge queues only in repositories owned by an organization, and `MichaelKinsy/PiG` is owned by a user account, so until the repository moves to an organization the variable is unset or `label`. Delete the variable to return pull requests to the full run.

Secret scanning in the queue runs TruffleHog over the commits the queue added. `gitleaks-action` supports only `push`, `pull_request`, `workflow_dispatch` and `schedule` and fails the job on `merge_group`, so that step is skipped there; the pull request run has scanned the same commits.
