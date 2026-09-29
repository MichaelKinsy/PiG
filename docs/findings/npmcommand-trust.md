# Package command trust investigation

Reference: Pi 0.87.1, `.upstream/v0.87.1`. Scope: `sec-npmcommand-trust`.

## Finding and exact reproduction

The prior `pt-nonhot-utils` handoff identifies `npm-command-trust-blocker.log`. Its retained fixtures contain a global `settings.json` with `packages: ["npm:fake-package"]` and an argv-style Node `npmCommand` that writes a global marker. The working directory contains `.pi/settings.json` or `.pig/settings.json` with a different Node `npmCommand` that writes a project marker. There is no saved trust decision. The command is `pi update --extensions` or `pig update --extensions`, not bare self-update.

The prior probe records Pi exit 0 with only the global marker and PiG exit 0 with only the project marker. The canonical reproduction added here is `test/parity/scenarios/project-trust/15-update-ignores-untrusted-npm-command.toml`. Its pre-fix artifact is `project install` for PiG and `global install` for Pi. The original base also rejects successful custom installers that do not create an inferred package directory. The artifact independently proves the command execution before that unrelated completion check fails.

The vulnerable call chain is `runPackageCommand` → `runUpdateCommand` → `updatePackages` → `installPackageArtifacts` → `installManagedNPM` → `defaultNpmCommand` → `runCmd`. `defaultNpmCommand` constructs a new, implicitly trusted `SettingsManager`. This reads the project command even if its caller has already resolved the project as untrusted. The same reconstruction occurs in npm removal, Git dependency installation and pnpm path lookup. Missing-package restoration and configured-resource discovery call these helpers too.

The impact is arbitrary code execution under the invoking user's account from an unapproved repository's `.pig/settings.json`. A global package need not belong to the repository. Looking up a missing pnpm package path can also execute the configured command without installing anything.

## Pi's contract

All source paths below are relative to `.upstream/v0.87.1/packages/coding-agent/src/`.

| Rule | Pi source |
|---|---|
| Start package command settings with project access disabled. | `package-manager-cli.ts:743-751` |
| Updates use the explicit approval override, or a saved `true` decision. They do not consult the default policy, prompt, or load trust handlers. | `package-manager-cli.ts:752-756` |
| Other package commands load global trust handlers before resolving trust. Explicit approval bypasses this bootstrap pass. | `package-manager-cli.ts:759-788` |
| Explicit override wins; then global extension result, saved trust, global default, and interactive prompt. An undecided noninteractive project is denied. | `core/project-trust.ts:45-94` |
| Saved decisions use the canonical working directory and nearest stored ancestor. | `core/trust-manager.ts:40-57` |
| Untrusted project settings load as an empty object. Trusted project settings override globals. | `core/settings-manager.ts:189-204,350,379-381,410-413,514-536` |
| The package manager retains the supplied settings manager. Its command getter uses that manager, not another file load. | `core/package-manager.ts:814-817,1747-1782` |
| Project-local install/remove require trust before package storage access. | `package-manager-cli.ts:927-940`; `core/package-manager.ts:1008-1011,1035-1038,1741-1744` |
| User-scoped package operations still use the merged command from a trusted project. Scope chooses storage, not command ownership. | `core/package-manager.ts:1747-1782,1815-1836,2025-2033` |
| Self-update alone selects the global `npmCommand`. | `package-manager-cli.ts:942,1067` |
| Missing global packages resolve before trust handlers load, while project settings remain disabled. | `core/resource-loader.ts:380-385,549-551`; `core/package-manager.ts:1271-1294` |
| Successful package installation completes after the command resolves; it does not require a guessed directory to exist. | `core/package-manager.ts:1181-1185,1815-1819` |
| Removal completes before its settings record is removed. | `core/package-manager.ts:1054-1056` |

The npmCommand trust correction is a parity fix, not a security divergence. The separately approved registry-cwd policy is D79. A global-only command override would be wrong: `18-approved-update-uses-project-npm-command.toml` proves that Pi uses the approved project command for a user package.

## Changes

`cmd/pig/package_command_trust.go` supplies the package-command settings once. The base predates the prior lane's command-trust work, so this file reuses that lane's `createPackageCommandSettings` logic. The installer-completion correction is also shared with that prerequisite. No unrelated utility changes or test-closure claims are imported.

The npm, Git dependency, resource-path, validation and missing-package helpers now receive the caller's settings manager. They cannot reload a project file or discard an explicit trust decision. The global-package bootstrap also installs missing global dependencies before loading trust handlers. The install/remove paired probes exposed this missing step; their artifact comparison retains both the bootstrap and requested command.

Removal now invokes the uninstaller before updating settings. The old order erased in-memory command overrides before execution and deleted the saved package on an uninstall failure. Git sibling-subdirectory retention ignores the source being removed while still preserving checkouts used by other sources.

The additive pre-session inspection context resolves saved/default trust without invoking extension handlers. Its auth inspection callers retain their registration-only contract. Full-suite testing caught an intermediate implementation that invoked trust handlers during auth discovery; the final implementation removes that regression instead of weakening the accepted auth tests.

### Helper audit

| Path | Result |
|---|---|
| npm install, update and removal | Use the supplied resolved settings. `--approve` and `--no-approve` remain command-local. |
| Git dependency install and update | Use the same manager for argv and for the plain-install versus `--omit=dev` choice, matching `core/package-manager.ts:1772-1778,1855-1857,1925-1927`. |
| Git removal | Runs no npm command. Shared-checkout preservation remains covered by the existing sibling-subdirectory tests. |
| pnpm path discovery, startup restoration and resource lookup | Retain the caller's settings through all helper calls. |
| Materializer and installer callbacks | Resolve trust at entry and pass the resulting manager through the installer. |
| Self-update | Already reads global `NpmCommand` in `cmd/pig/self_update.go` and `internal/codingagent/selfupdate_tier.go`; no project-command fix is needed there. D39 still owns native update transport. |
| Registry settings | Neither Pi's nor PiG's settings schema has an `npmRegistry` key. PiG's explicit registry selector belongs to a package source. Untrusted project package lists cannot supply it. `TestPackageRegistrySourcesRequireProjectTrust` covers that boundary. Existing custom-registry tests retain explicitly supplied registry behavior. |
| Available-update metadata checks | `getLatestNpmVersion` now retains the selected command and uses the same scoped lookup for explicit updates and notifications. User-package lookups use managed storage under D79. Trusted project lookups retain Pi's cwd. |

## Native registry risk and approved policy (D79)

The following table records the initial probe before the D79 implementation. A loopback-only audit uses real Pi, PiG and npm. Each process receives a fresh agent directory, a saved `false` project trust decision, a user npm configuration pointing at `/user/`, and a project `.npmrc` pointing at `/project/` on the same local HTTP server. The server serves the package metadata and tarball; no package request leaves the machine. The managed package has version `1.0.0`, and the server advertises that same version.

| Command | Pi 0.87.1 package metadata request | PiG package metadata request |
|---|---|---|
| `install npm:fake-package` | `/user/fake-package` | `/user/fake-package` |
| `update npm:fake-package` | `/project/fake-package` | `/user/fake-package` |
| `remove npm:fake-package` | No package metadata request | No package metadata request |

All six commands in that initial probe exit 0. Pi only checks the current version; PiG unconditionally installs again at that point. Do not interpret the initial probe as general update parity. The evidence consists of `registry_probe.py` and `registry-probe.log` in the lane's retained artifacts.

Pi's metadata lookup explicitly invokes `npm view` in the invoking project's cwd (`core/package-manager.ts:1151-1159,1511-1519`). `runCommandCapture` passes that cwd to the child (`core/package-manager.ts:2613-2633`). Native npm reads that directory's `.npmrc` even when Pi denies `.pi` settings. Managed install and removal pass `--prefix` (`core/package-manager.ts:1812,1832`), and the probe shows npm selects the user registry on those paths. Git dependency installation instead runs in the selected checkout (`core/package-manager.ts:1855-1857`), not the unrelated invoking project.

An untrusted project can therefore redirect Pi's metadata lookup, learn queried package names, and influence the advertised version or failure result. This probe does not demonstrate credential theft or arbitrary code execution through registry metadata. It is distinct from PiG executing a project-authored `npmCommand`.

Owner Michael Kinsy approved option A on 2026-09-27: perform metadata lookup for a user package from its managed install root, while retaining the user's npm configuration and the explicitly selected command. D79 records that security divergence. Trusted project-scoped packages continue to use the project cwd. The implementation does not scrub environment configuration or replace a selected npm, pnpm or Bun command with literal npm.

The explicit update path now performs Pi's metadata check instead of unconditionally reinstalling a package. A current or older advertised version skips installation. A lookup failure retains Pi's install fallback. Exact versions remain pinned; mutable tags and ranges remain eligible. `PI_OFFLINE` uses Pi's exact truthy values, so `0`, `false` and padded values do not suppress the lookup. These source corrections follow `core/package-manager.ts:53-65,1150-1164,1481-1530` and are not additional divergences. General update batching and the complete package-manager test suite remain outside this scoped closure.

The new repeatable loopback scenarios require one `view` through the selected command and no install for a current package. An unconditional reinstall cannot satisfy the assertions. Scenario `19-user-package-registry-isolation` records `metadata cwd=user; registry=user; selected-command=yes; installs=0` for PiG and `metadata cwd=project; registry=project; selected-command=yes; installs=0` for Pi. Scenario `20-trusted-project-package-registry` produces the latter line identically for both. Both scenarios run three pairs. `TestNpmMetadataLookupScope` covers npm, pnpm and Bun argv in both scopes; refusal tests cover inaccessible managed storage and denied project lookup without falling back to the invoking cwd.

## D79 verification

The `registry-policy` evidence set retains the red unit and paired failures before metadata lookup is wired into updates. The unsafe-cwd mutation restores the project registry for a user package and fails scenario 19. The all-user-cwd mutation changes a trusted project package to the user registry and fails scenario 20. Both mutations compile before their behavioral failure. Scalar/array metadata, newer/current/older versions, lookup failure fallback, exact pins, mutable tags, offline values, denied projects and inaccessible or initially missing managed roots have unit guards. The complete `cmd/pig` and embedded-doc packages, both affected parity families, native/Windows vet and touched-package lint pass. Inventory and coverage are regenerated. The repository-wide blockers listed below remain separate from this approved policy.

`BenchmarkNpmMetadataLookup` exercises the real selected Node subprocess and captures CPU/allocation profiles. The sample is approximately 37 milliseconds, 69,952 bytes and 223 Go allocations per lookup; it excludes external-process memory and makes no speedup claim. Metadata subprocesses are awaited, use Pi's existing 10-second network budget and are joined by `exec.Cmd.Output`; no new detached task is introduced.

## Verification and limits

- `unit-red.log` records execution of the project command instead of the global command at the installer, remover, updater, restoration and pnpm lookup boundaries.
- `pair-red.json` and its artifacts retain the real-Pi comparison before the fix.
- `reload-mutation-unit.log` and `reload-mutation-pair.json` restore the unsafe file reload. They compile and fail, including Git dependencies, and all three untrusted-command scenarios fail.
- `global-only-mutation-unit.log` and `global-only-mutation-pair.json` force global settings. They compile and fail the trusted/override controls and the approved-update scenario.
- `bootstrap-red.log` retains the missing global-package bootstrap difference. The final install/remove scenarios compare the complete invocation artifact without deleting that extra command.
- `remove-order-red.log` and `remove-order-mutation-pair.json` prove override retention and the record-before-uninstall ordering. The mutation reports `recorded=false` where Pi reports `recorded=true`.
- `context-red.log` proves the pre-session inspection path previously used denied project commands. `inspection-green.log` also passes the existing auth registration-only tests.
- `project-trust-final.log` and `cli-utils-final.log` pass both affected families with their declared durability. The four new scenarios run three pairs each and use artifact equality plus independent expected labels. No new normalization, timeout increase, skip or lint suppression is added.
- `package-tests-final.log` passes the complete `cmd/pig` package after the auth inspection correction. The final focused tests also cover the updated approval help text. Native `go vet ./...`, Windows vet for `cmd/pig`, changed-package lint and full `make lint` pass.
- `BenchmarkPackageCommandTrustResolution` measures the saved-trust command-settings path, with CPU and allocation profiles retained. The measured sample is approximately 34 microseconds, 4,976 bytes and 31 allocations per operation. This is a resource-cost observation, not a speedup claim. Package subprocess calls remain awaited; temporary trust extension hosts close before the command continues. No new background task or TUI-loop IPC is introduced.

Repository gates still have unrelated blockers. `ci-contracts` reaches the existing hot-path test-porting release gate and reports 354 findings. `ci-drift` reaches D78's missing `SCRUTINIZED:approved` marker. The separately invoked source-hygiene gate reports existing private operator paths in `test/parity/unit-evidence/fix-ext-ui-state.md` and `fix-node-scrollview.md`. The full test run also reports the three D78-related closure dashboard failures and a Node stdin `EAGAIN` in `tui/TestColorDetectionMatchesPi`. These findings are not suppressed or counted as passing gates. The intermediate auth inspection failures are fixed and are not classified as baseline failures.

The upstream test mappings remain partial. This lane adds targeted regression evidence, not a claim that all package-manager tests or all update semantics are ported. No upstream test hash, hot-path tag or release baseline is changed. D79 is the sole new approved divergence.
