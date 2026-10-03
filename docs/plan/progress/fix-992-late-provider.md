# fix-992-late-provider: providers registered after the factory finished keep their operations, as in Pi

Base: `staging/porter/pi-0.99.1` @ 94f45865e (upstream pin 0.99.2), plus `staging/fix-992-sdk-surface` @ 0b6f6f92f (merge commit): the Node typed provider operations (`providerOperations`, `providerOperationAPIs`) this lane extends are not on the base yet. Source: the lead's answer to question 1 of the fix-992-sdk-surface lane.

## Contract (upstream 0.99.2)

- `ExtensionAPI.registerProvider(name, config: ProviderConfig)` takes the whole config at any time, callbacks included: `.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803` ("During initial extension load this call is queued ... After that it takes effect immediately"), `ProviderConfig` with `streamSimple`, `images`, `classifiers`, `oauth` at `types.ts:1875-1925`.
- Loader and runner: `loader.ts:449-457` (`registerProvider` -> `runtime.registerProvider(name, config, extension.path)`), `loader.ts:213-218` (queued before bind), `runner.ts:517-523` (after `bindCore` the runtime calls the registry directly with the whole config).
- Registry: `core/model-runtime.ts:753-787` (merge of the defined values over the previous root) and `core/provider-composer.ts:632-663` (images and classifiers run through the composed provider).

Pi therefore allows it; there is nothing to ask the owner.

## Cause

The `registerProvider` host call of every SDK carries `{name, config}`. The register payload's `ProviderDecl` also carries `stream_simple`, `image_apis` and `classifier_apis`, and the host's `attachProviderOperations` turns them into the callbacks of the `ProviderConfig` (`provider_operations.go`). `handleProviderRegistrationCall` (host_calls.go) never calls it, so a late provider has none. The SDKs add to this: Go `json.Marshal`s the config (callbacks fail: `unsupported type`), Python fails with `not JSON serializable`, Rust `ModelRegistry::register_provider` has no operations parameter, and Node strips `streamSimple` but does not name it, `images` or `classifiers` in the call.

## Red (tests with signature stubs)

Commit `test(late-provider): port upstream 0.99.2 late provider registration tests with signature stubs (red)`.

Pi has no test of a late registration that carries operations. The ported Pi rows that bind it are already in the tree: `extensions-runner.test.ts` "post-bind register and unregister take effect immediately" (`coding/extension/host/inproc/upstream_provider_queue_test.go:156`), `agent-session-dynamic-provider.test.ts:138` "applies command-time registerProvider overrides" (`coding/dynamic_provider_upstream_test.go`), `model-runtime-images.test.ts` "registers extension image and classifier models with their implementations" (`coding/model_runtime_images_upstream_test.go`) and `8964-extension-provider-streaming.test.ts`. The new rows compose them: the same config as the factory registration of those tests, registered by a command after load.

New tests:

- `coding/late_provider_subprocess_test.go` (`TestLateProviderRegistrationKeepsOperationsThroughRuntime`, 4 subtests; fixture `coding/testdata/late-provider.mjs`): the real Session, Model Runtime and Extension Host with a Node extension. `pi.registerProvider` and `ctx.modelRegistry.registerProvider` from a command; the Model Runtime's `GenerateImages`, `Classify` and `Complete` run the callbacks; a second registration without operations keeps them; `unregisterProvider` removes the provider.
- `coding/extension/host/subprocess/late_provider_registration_test.go` (`TestLateProviderRegistrationCallWiresDeclaredOperations`): the host call's declaration becomes the config's `StreamSimple`, `Images` and `Classifiers`; a call that declares nothing wires nothing.
- `coding/extension/host/subprocess/runtime_node_model_types_test.go` (`TestNodeLateProviderRegistrationDeclaresItsOperations`): the Node runtime's late call names the operations, through `pi.registerProvider` and `ctx.modelRegistry.registerProvider`.
- Go SDK `extensions/sdk/late_provider_test.go` (4 tests), Python `extensions/sdk-py/tests/test_late_provider.py` (4 tests), Rust `extensions/sdk-rs/tests/model_types_wire.rs` (3 tests): the call's args, the callbacks staying in the extension and running on `provider_operation` and `provider_stream_simple`, the merge, the unregistration, a callback of the wrong type.
- `test/extension-conformance/late_provider_test.go` (3 tests x 9 SDK placements: Go fused, strict, packed; Rust isolated, packed; Python strict, shared-ok; Node isolated, packed): one `late_provider` tool per SDK registers the provider through `ctx.modelRegistry`; the host's `ProviderConfig` must run images, classifiers and streamSimple in the extension with values only the callbacks produce. The rig's `recordProvider` is also the bound runner's `RegisterProvider` (a late call goes there, not to `SetProviderCallbacks`).

Stubs: Rust `ProviderOperations::stream_simple` (stores nothing), `ProviderStreamSimpleFn`, `ModelRegistry::register_provider_operations` (sends `registerProvider` without a declaration). No other family's code is stubbed.

Red runs (behavior failures, no compile errors):

- `coding`: `TestLateProviderRegistrationKeepsOperationsThroughRuntime`: `Provider late-pi does not support image generation` (3 subtests fail; the unregister subtest passes today and is mutation-checked below).
- `coding/extension/host/subprocess`: `TestLateProviderRegistrationCallWiresDeclaredOperations`: `a declared streamSimple was dropped`; `TestNodeLateProviderRegistrationDeclaresItsOperations`: `streamSimple is declared` (`undefined`).
- `extensions/sdk`: 4 tests fail (`json: unsupported type: sdk.ProviderClassifyFunc`, no call made).
- `extensions/sdk-py`: 4 tests fail (`Object of type function is not JSON serializable`).
- `extensions/sdk-rs`: `registry_register_provider_keeps_operations_after_the_factory` and `registry_register_provider_merges_operations_like_the_factory` fail (no `stream_simple`/`image_apis` in the call); `registry_unregister_provider_drops_its_operations` passes against the stub.
- `test/extension-conformance`: `TestLateProvider*` fail in all 9 placements x 3 tests: Go `unsupported type`, Python `not JSON serializable`, Rust and Node `the late provider lost an implementation: images = map[], classifiers = map[], streamSimple set = false`.

## Green

Commit `feat(late-provider): a provider registered after the factory keeps images, classifiers and streamSimple in every SDK (green)`. One cause, two fixes:

- Host (`coding/extension/host/subprocess/host_calls.go`): `handleProviderRegistrationCall` decodes the call as the register payload's `ProviderDecl` and runs `attachProviderOperations` before `RegisterProvider`, so `stream_simple`, `image_apis` and `classifier_apis` wire the same callbacks as in a factory registration. One declaration shape, no new wire field.
- SDKs: each keeps the callbacks per provider in the extension and names them in the call.
  - Go: `Extension.RegisterProvider` and `ModelRegistry.RegisterProvider` share `takeProviderDef` (this includes `oauth` closures; `dispatchOAuth` now reads under `providerMu`).
  - Python: `Extension.register_provider` and `ModelRegistry.register_provider` share `_take_provider_declaration`; callables and JSON-ability are validated before anything is stored, so a TypeError stores nothing.
  - Rust: `ProviderCallbacks` (new, `provider.rs`) is the one map of implementations. It replaces `provider_operations` and `provider_streams`; `register_provider_stream` is `register_provider_operations` with `ProviderOperations::stream_simple`. `ModelRegistry::register_provider_operations` is the late call.
  - Node: `providerDeclaration(name, config)` builds the register-frame entry and the late call's args.
  - All three native SDKs restore the held callbacks when the host refuses a registration (`model-registry.test.ts:1320-1343`) and drop them after the host unregistered the provider.
- Docs: `docs/extension-api-parity.md` (typed model registry contract, `ProviderConfig.images/classifiers` row, provider producer row).

One edit to a red-commit test, my own and not a Pi port: the Rust late-stream row read the response before the `provider_stream_event` notification that precedes it; it now reads the notification first.
Also added after green: `TestModelRegistryRefusedRegistrationKeepsTheHeldCallbacks`, `test_model_registry_refused_registration_keeps_the_held_callables`, `registry_refused_registration_keeps_the_held_operations` (a guard for the restore-on-refusal behavior, not red-proven first; mutation-proven below).

## Evidence

- Green: `go test ./coding` ok; `go test ./extensions/sdk` ok with `-race` (skipping the theme lane's `UITheme` tests); `cargo test --test model_types_wire` 12/12; `pytest tests/test_late_provider.py` 5/5; `go test ./test/extension-conformance -run TestLateProvider` 27 rows (3 tests x 9 placements) ok; lint `0 issues`; `go vet`, `GOOS=windows go vet`, `gofmt`, `go fix -diff` clean; `check-public-claims.py` ok.
- Load: `GOMAXPROCS=4 go test -race -count=24` of the Go SDK, host and Node-runtime tests and of the `coding` real-path test; on cores 0-3 with four recorded CPU burners `-race -count=3` of `TestLateProvider` (27 rows each, ok), `-count=12` of the Go SDK and `coding` tests, Rust `--test-threads=4` ok.
- Mutation (each fails its named test, then restored): Go `restoreHeldProviderCallbacks` removed from refusal -> `TestModelRegistryRefusedRegistrationKeepsTheHeldCallbacks`; removed from `UnregisterProvider` -> `TestModelRegistryUnregisterProviderDropsItsOperations`; Python restore removed (both sites) -> `test_late_provider.py`; Rust restore removed (both sites) -> `registry_refused_...` and `registry_unregister_...`; host `attachProviderOperations` removed from the late call -> `TestLateProviderRegistrationCallWiresDeclaredOperations`; Node `providerDeclaration` replaced by `{name, config}` -> `TestNodeLateProviderRegistrationDeclaresItsOperations`.

## Not run or failing for reasons outside this lane

- upstream 0.99.2 npm package absent (`extensions/sdk-ts/node_modules`): `coding/extension` `TestExecCommandRejectsInvalidArgumentsAsPiDoes`, about 40 `coding/extension/host/subprocess` tests that import the pinned Pi package (`TestNodeRegisteredProviderConfigMatchesPi` and the vendoring checks), `test/extension-conformance` `TestSelectedToolsPiContract`, and the whole `make parity-family FAMILY=extensions-runtime` (the `pi` reference binary is missing; 8 of its scenarios fail at `runner_test.go:106`). Xvfb is missing for `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux`.
- The theme tests of `fix-992-sdk-theme` (`TestUITheme*`, `TestNodeThemeAppearanceColorsAndStyle`, `TestThemeAcrossSDKs`, Python and Rust theme tests, 5 sdk-surface problems) stay red; they are not in this lane.
- `make generate` stops at `known-gaps` on the unapproved `designedOutCases` (Q2 of fix-992-sdk-surface); the rest regenerated.

## Deferred, and why

- Q1, closed by the review (rev-fix-992-late-provider) on the lead's answer: Go `Extension.RegisterProvider`/`UnregisterProvider` and Python `register_provider`/`unregister_provider`/`register_oauth_provider` send the host call once the extension runs, through the path `ModelRegistry` uses; Rust documents `ctx.model_registry()` as the late door, and a queue call after `run` does not compile (compile-fail doctest).
- Q2, left on the lead's answer: a late registration does not hold the host's liveness (`releaseLiveness`) for a connection that had no provider at load. The factory path holds it only for heartbeat while idle; operations in flight are already work. Not changed.

## Stubs for other families

None.
