// SPDX-License-Identifier: MIT

// Package cidag implements the fail-closed affected-package and affected-lane
// dependency DAG selector for PiG CI (see CI-DAG-DESIGN.md).
//
// # Workflow shadow hook (for .github/workflows/ci.yml)
//
// To enable shadow-mode plan publication without changing any gating behavior,
// pass the repository variable PIG_CI_DAG and pull-request SHAs to the existing
// `automation/ci/ci-plan.sh` step inside the `changes` job:
//
//   - name: Plan the jobs this event runs
//     id: plan
//     env:
//     EVENT: ${{ github.event_name }}
//     MODE: ${{ vars.PIG_PR_CI_LIGHT }}
//     FULL_REQUESTED: ${{ contains(github.event.pull_request.labels.*.name, 'ci:full') || github.event.action == 'ready_for_review' || steps.classify.outputs.os-specific == 'true' }}
//     PIG_CI_DAG: ${{ vars.PIG_CI_DAG }}
//     PIG_CI_DAG_BASE: ${{ github.event.pull_request.base.sha || github.event.merge_group.base_sha }}
//     PIG_CI_DAG_HEAD: ${{ github.event.pull_request.head.sha || github.event.merge_group.head_sha }}
//     PIG_CI_DAG_PLAN_OUT: ci-plan.json
//     shell: bash
//     run: automation/ci/ci-plan.sh "$EVENT" "$MODE" "$FULL_REQUESTED" | tee -a "$GITHUB_OUTPUT"
//
// When PIG_CI_DAG is unset or "off", ci-plan.sh is byte-identical to its static
// behavior. When PIG_CI_DAG is "shadow", ci-plan.json and $GITHUB_STEP_SUMMARY
// record the DAG selection while the emitted matrices stay 100% unchanged.
// Shadow-mode empirical edge audits run `go run ./automation/ci/cidag/cmd/cidag
// -audit-run -base <base> -head <head> -failed-lanes <l1,l2>` to flag any blind
// spot where a full run failed in a lane that the DAG plan skipped.
package cidag
