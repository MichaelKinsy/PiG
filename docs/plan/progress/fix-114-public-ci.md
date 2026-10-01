# fix-114-public-ci progress

Lane: make PR #114 green on public CI. CI mirrors only `.upstream/v0.99.2` (`current` -> `v0.99.2`); the lane's worktree `.upstream` was reduced to the same layout (symlinks to the shared mirror removed in the worktree only).

## Failing jobs and root causes (from /tmp/fix-114-public-ci-logs)

| Job | Root cause |
|---|---|
| Linux / test-fast, test-subprocess, parity; Node extensions x3; macOS native; Windows | tests read `.upstream/v0.87.1/...` (absent on CI) |
| Linux / contracts | `test-porting-release` cannot read baseline commit `a0611dc4a7...` (staging-only) |
| Source assurance | `make compliance` `pins`: ci-parity image pins Pi 0.87.1 |
| Build parity image | `pi --version` check in build-ci-image.sh compares the image's Pi (0.87.1) with the pin (0.99.2), exit 1 with no message |
| Build go image | `apk add` of `automation/images/ci-go/packages.lock` fails: Alpine 3.24 moved `nghttp2-libs` 1.69->1.70 and `pcre2` 10.48->10.49 |
| Secret scanning | gitleaks generic-api-key on an `Anthropic-Beta` header-name list |
| Windows | `EADDRINUSE` matched by Linux text |
| Startup latency | see below |

## Red

- `test/parity/upstreamreads`: tree-scanning guard (`TestNoVersionedUpstreamReadsInCode`) plus scanner self-tests. Red run: 33 findings.

## Green (all pushed to the lane branch)

1. Versioned `.upstream` reads: 30 code sites now read `.upstream/current` (the repo convention); guard `test/parity/upstreamreads` passes (scans git-listed go/mjs/cjs/js/ts/rs/py/sh/toml/yml/mk/Makefile/Dockerfile, ignores comments, docstrings, citations). `ai/testdata/stream-cases.json` equals the extractor output byte for byte (checked with the real Pi 0.99.2 pi-ai).
2. ci-parity pinned to 0.99.2 (Dockerfile, metadata.env, package.json, lock from npm 12.1.0). `make compliance` passes. `build-ci-image.sh parity` built locally and passed every version check, including `pi --version`; that check is the silent exit 1 of "Build parity image". ci-go lock = public main's refresh (#113); `build-ci-image.sh go` built.
3. test-porting baseline: `3528e21da` (PR #114 head, public via refs/pull/114/head), ratcheted with `automation/gen/test-ported-baseline.py` to 586 ported paths. A public-main commit cannot be the anchor: 2ab6f9fcb has no 0.99.2 mapping (gate requires one). v0.99.1/v0.87.1 policies are read by no gate (only the current version's policy is); v0.99.1 still names staging-only 0ab6ad89 and was left alone. **The gate still fails behind the baseline read on owner decisions** (see Questions).
4. gitleaks allowlist covers `interleaved-thinking|oidc-federation|oauth` dated beta names; verified with a gitleaks 8.24.3 build: old config finds 1, new finds 0.
5. Windows: test matches Pi's `code: "EADDRINUSE"` through `nodeerrno.ErrorCode` (WSAEADDRINUSE 10048 maps to EADDRINUSE); `GOOS=windows go vet` clean, not run on Windows here.
6. Build parity image: pin drift as in 2.
7. Startup latency: advisory job (`continue-on-error: true`); exit 2 only because `none` warm/cold = 1.064/1.068 against 1.05. Pi columns were fine at 0.99.2 (ts 0.862, package 1.011/1.003 vs 1.15), so no missing package or pin. Not reproduced on quiet CPUs (4-CPU taskset, 25 runs, v0.2.0 vs this tip): warm 0.996/0.995, cold 1.031/1.014. Package init is equal (inittrace 25.6-27.8 ms vs 27.6-27.9 ms, 6.7 MB vs 7.2 MB). No code change. Not proven to be pure noise: cold is consistently +1-3%.
8. CodeQL XSS (callback.go:200): the production hook (`ai.renderOAuthPage`) already escapes title, heading, message and details with `html.EscapeString`, as Pi's `escapeHtml` does; `RenderPage` is Pi's own caller hook, so the alert is the analyzer following request data through a function value (test hook echoes `page.Message`). Added `TestOAuthCallbackPageEscapesRequestValues` (real server, `<script>` in error_description and in error); mutation (drop the details escape) fails it. No library change: escaping at the boundary would double-escape the production page or diverge from Pi (see Questions).
9. llama: `positiveSafeInt(value, math.MaxInt)` bounds the int conversion; `TestPositiveSafeIntStaysWithinTheIntRange` (fails without the bound; PiG does not build for 32-bit, so the helper takes the limit).
10. `maxSeenKeyHint` (schema_object_order.go) and `codemodeLinesHintLimit` (tool_codemode_renderer.go) cap the capacity hints; tests drive 65,636 keys / calls past the cap. These two tests pass on the old code too (behaviour is unchanged); they pin order and count at the cap.
Extra CI-only failures found in the logs and fixed: four theme tests assumed truecolor (CI has no COLORTERM/HERDR) — they now pin the capability (reproduced locally with a clean terminal environment); `ai/openai.go` gofmt (staging merge).

## Evidence
- CI-like layout: worktree `.upstream` holds only `v0.99.2` and `current`. Failing tests rerun there with terminal hints unset: ai, cmd/pig, tui, internal/codingagent(+compaction), subprocess targeted tests pass.
- `go test -race -count=1`: ai, tui, coding/mcpext, llama, compaction, upstreamreads. `-race -count=24 GOMAXPROCS=4 taskset 4 cores + 4 burners`: new tests (burner PIDs recorded in /tmp/fix-114-public-ci/burners.pid, all gone).
- `make parity-family FAMILY=extensions-runtime`: 95 scenarios ran, incl. 66-session-replacement-lifecycle. `make compliance`: pass. `go vet`/`GOOS=windows go vet`/golangci-lint/`go fix -diff`: clean for touched packages.
- Mutation checks: details-escape removed (mcpext test red), `float64(limit)` bound removed (llama test red).

## Not verifiable here
- Xvfb is not installed on the lane host: `TestUpstreamNativeX11Transfers`, `TestNativeX11ExplicitTCPTransport`, `TestNativeClipboardProcessExitWithBlockedAuthority`, `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` fail here before reaching the fixture read. `clipboard-x11-test.c` from `.upstream/current` compiles with the test's cc flags and is identical to the 0.87.1 file.
- Windows run of the EADDRINUSE test; macOS native job.
- Contracts job beyond the baseline read (below).

## Stubs for other families
None.

## Review fixes (rev-fix-114-public-ci)
- Item 8: PiG's page was not Pi's: `html.EscapeString` spells `"` as `&#34;` where Pi's `escapeHtml` writes `&quot;`, and the template had drifted (condensed CSS, missing emoji fonts, `main` justify-content, `h1` color, the empty details slot). `ai/oauth_page.go` now emits Pi's template and entities; `TestOAuthPageMatchesThePinnedPackage` compares every page with the pinned `pi-ai` `dist/utils/oauth-page.js` byte for byte.
- Item 10: the `1 << 16` caps were new `divergence-guard` magic-literal hits. The hints now use the length alone (no arithmetic, so no overflow); the tests keep their assertions at 5,000 keys and calls.
- The guard also scans `.mts`, `.cts`, `.tsx` and `.jsx`.
