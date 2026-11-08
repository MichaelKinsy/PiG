# follow/chord-ui rows (FollowChordUI): PORT_MAP rows, D records, test mapping, async contracts

Pi main c23b9ec1. Integrator transcribes into PORT_MAP / DIVERGENCES / test-mapping (this lane does not edit them).

## PORT_MAP rows (status `partial` until reviewed)

| Upstream | PiG | Status |
|---|---|---|
| `packages/chord/src/surfaces/language.ts` | `chord/next/surfaces/surfaces_language.go` | partial (inLanguage/currentLanguage designed out, D450) |
| `packages/chord/src/surfaces/translations.ts` | `chord/next/surfaces/surfaces_translations.go` | partial |
| `packages/chord/src/surfaces/messages.ts` | `chord/next/surfaces/surfaces_messages.go` | partial (type-level checks designed out, D450) |
| `packages/chord/src/surfaces/format.ts` | `chord/next/surfaces/surfaces_format.go` | partial: formatNumber ported; **formatTime pending** (no CLDR date patterns in Go; cases `formats times ...`, `formats in the platform's default locale outside a render` (time half) and the formatter-collection case not ported) |
| `packages/chord/src/surfaces/page-state.ts` | `chord/next/surfaces/surfaces_page_state.go` | partial (D451) |
| `packages/chord/src/surfaces/active-language.ts` | `chord/next/surfaces/surfaces_page_state.go` (`HostLanguage`) | partial: `followActiveLanguage` pending (needs preference/LocaleChoice) |
| `packages/chord/src/tui/scope.ts` | `chord/next/tui/tui_scope.go` | partial (D452) |
| `packages/chord/src/surfaces/{preference,locale-choice,theme-choice,themes,active-theme}.ts` | pending | pending |
| `packages/chord/src/{telemetry.ts,telemetry-ingest/**}`, spans in host/remote/jobs/link/store/http, `telemetry-sqlite` v2 | pending (T6) | pending |
| `packages/chord/src/node/app/**` (T10), `node/surfaces/**`, `node/tui/**` (T11), client `web`, `react`, `tui/{runtime,bridge,channel,frames,terminal,theme}`, `surfaces/{client,connection,loaded,registered,scripts}` | pending; homes in specs/chord-ui-homes.md | pending |

## Test mapping

- `test/surfaces/language.test.ts` -> `surfaces_language_test.go` (all cases; plus `TestCreateLanguageRefusesATagThatIsNone` for the Intl.Locale throw).
- `test/surfaces/translations.test.ts` -> `surfaces_translations_test.go` (all).
- `test/surfaces/messages.test.ts` -> `surfaces_messages_test.go` (all but the `@ts-expect-error` type cases: designed out, D450).
- `test/surfaces/format.test.ts` -> `surfaces_format_test.go` (number cases only; time cases pending).
- `test/surfaces/page-state.test.ts` -> `surfaces_page_state_test.go` (all).
- `test/tui/scope.test.ts` -> `chord/next/tui/tui_scope_test.go` (all 11 cases, the call being a Go func).
- Pending: `test/surfaces/{preference,locale-choice,active-language,themes,active-theme,client,connection,registered}.test.ts`, `test/tui/frames.test.ts`.

## D records (owner sign-off pending)

## D450 Messages render in an explicit language; the type-level checks of defineMessages are not enforced

What: Pi renders messages in a module-wide "current language" set by `inLanguage` around a synchronous render; message functions call `formatNumber`/`t(...)` and get it implicitly. PiG's `MessageFunc` receives the `*Language` as its first parameter and passes it to `FormatNumber(lang, ...)` and `Messages.In(lang)`. Pi's compile-time checks (every catalog has the first's keys and signatures, typed `t`, `DataKeys`) have no Go form; DefineMessages checks what Pi checks at run time, and an unknown key is an error return. `currentLanguage`/`inLanguage` are not ported.
Why: Go has no goroutine-local state, and a package-level variable would be wrong across goroutines and hosts (Pi's own text says no module-wide setting).
Scope: `chord/next/surfaces`. Observable output of a render is the same.
Owner decision: owner sign-off pending.
Evidence: `TestRendersAPlainTInsideAMessageFunctionInTheLanguageUnderWay`.

## D451 Page state of a host is dropped by a runtime cleanup

What: Pi's `WeakMap<Host, Map>`; PiG keeps a map keyed by the host's address (a uintptr, holding nothing alive) and drops the entry with `runtime.AddCleanup` on the host. Host must be a pointer, as CreateHost returns. `SameValue` stands for `===`: comparable values by `==`, maps/slices/funcs/chans by identity.
Evidence: `TestPageStateIsCollectedWithItsHost`.
Owner decision: owner sign-off pending.

## D452 TuiScope's Go form

What: methods cannot be generic, so `watch` is `WatchMember`/`WatchNative`/`WatchJSON`, `registry` and `optimistic` are functions; `add(action, promise)` is `add(action, call func() error) error` running the call; a reader returns `(value, ok)` for `undefined`; `messages` returns `*BoundMessages`; a scope's watches are stopped synchronously in Dispose (a context's AfterFunc ends them on another goroutine, which races with the host's close in registries/state: reported to the chord owner, not fixed here).
Evidence: tui_scope_test.go (11 cases, `-race -count=3`).
Owner decision: owner sign-off pending.

## Async contracts (test/parity/async-contracts.toml rows to add)

- `surfaces/active-language.ts followActiveLanguage`, `tui/scope.ts watch/registry/optimistic`: unawaited `.then` chains are goroutines owned by the scope; ended by Dispose (`stop` joined before return).

## Update (preference, locale choice, themes, active language/theme landed)

- `surfaces/{preference,locale-choice,theme-choice,themes,active-language,active-theme}.ts` -> `surfaces_{preference,locale_choice,theme_choice,themes,active_language,active_theme}.go`, partial. Tests mapped 1:1 (`preference`, `locale-choice`, `active-language`, `themes`, `active-theme` test files: all cases). `go test -race -count=3` green.
- Go form notes (extend D450/D452): ThemeVariables returns a map (order is the caller's); Theme records are maps; Follow* take a `context.Context` last and end their watches in Stop.
- Still pending in surfaces: `client.ts`, `connection.ts`, `loaded.ts`, `registered.ts`, `scripts.ts` (client-side; vendored per specs/chord-ui-homes.md), formatTime.

## Update 2 (telemetry package, T6 first part)

- `packages/telemetry/src/memory.ts` (telemetryNow, microsecond stamps) -> `telemetry/memory_now.go` + `memory.go`; `render.ts` (control-character escape, microsecond lengths) -> `render.go`; `internal/jsclock.TimeOrigin`. Tests: `telemetry_now_test.go` (2 cases of store.test.ts), 2 render cases added to `render_upstream_test.go`. `go test ./telemetry/` green after `run.sh --prepare` (Pi 1.5.0 oracle tests pass; they do not yet cover the new clock: oracle must move to Pi main with the pin).
- Noticed (not changed): `telemetry/memory.go` AddEvent appends `eventTimes` twice per event (lines ~340-341), so `eventTimes[index]` misaligns after the first event.
- Still pending T6: chord `telemetry.ts`, host/remote/jobs/link/store/http spans, `telemetry-ingest`, `telemetry-sqlite` v2.
