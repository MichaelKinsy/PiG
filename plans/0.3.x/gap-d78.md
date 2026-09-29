# Lane gap-d78 plan: SDK Provider object carriers and foreign registered-configuration identity (D78)

Base: staging `release/0.3.0-base2` (`606595120`). Upstream: Pi 0.87.1 in `.upstream/v0.87.1`. Branch: `team/smc1/gap-d78`.

This plan touches `extensions/sdk`, `extensions/sdk-rs`, `extensions/sdk-py`, the Node runtime and `extensions/sdk-ts` declarations.

## 1. Pi's observable contract

All paths are relative to `.upstream/v0.87.1/packages/`.

### 1.1 Registration and retrieval

- `coding-agent/src/core/extensions/types.ts:325` gives every extension context `modelRegistry: ModelRegistry`. `:1604-1620` declares `pi.registerProvider(provider)`, `pi.registerProvider(name, config)` and `pi.unregisterProvider(name)`.
- `coding-agent/src/core/extensions/loader.ts:182,206-213` queues name/config registrations before bind; `:421-433` applies them through `applyRuntimeChange` after bind. `extensions/runner.ts:443-459` flushes the queue in order, reporting a thrown validation error as `register_provider` without stopping later registrations. `:480-499` installs the immediate post-bind path. `agent-session-services.ts:158-169` flushes the same queue for SDK-created sessions.
- `coding-agent/src/core/model-registry.ts:101-103` `getProvider` returns `runtime.getProvider`, the composed Provider object held by the `Models` collection (`model-runtime.ts:389-391`). `:147-156` dispatches the overloads. `:162-171` returns `getRegisteredProviderConfig`, `getRegisteredNativeProvider` and `getRegisteredProviderIds` from the runtime without copying.
- `model-runtime.ts:438-448`: `getRegisteredProviderConfig(id)` returns the stored effective object; `getRegisteredNativeProvider(id)` returns the author's original Provider object; `getRegisteredProviderIds()` is the insertion-ordered union of both maps.
- `model-runtime.ts:744-751` `registerNativeProvider`: rejects an empty trimmed id, deletes any name/config registration, stores the original object, recomposes, updates the snapshot and starts an unawaited offline refresh.
- `model-runtime.ts:753-789` `registerProvider(id, config)`:
  1. validates only the incoming `config` against the builtin and models.json layers (`validateExtensionProvider`), throwing before any state changes;
  2. deletes a native registration with the same id;
  3. builds `effective = { ...previous }`, then copies each own enumerable key of `config` whose value is not `undefined` (`Object.entries`, so getters run once, in property order, and symbol keys are skipped);
  4. stores `effective` and recomposes. The root is a fresh object on every call; its children (models array, `oauth` object, functions) are the author's originals;
  5. marks a stored or request-auth-configured provider provisionally available;
  6. starts `void this.refresh({ allowNetwork: false })`.
- `model-runtime.ts:791-797` `unregisterProvider` deletes both registrations, recomposes, updates the snapshot and starts the offline refresh.

Consequences the reader can observe inside one heap:

- Two `getRegisteredProviderConfig(id)` calls between registrations return the same object (`===`). A re-registration yields a new root; a previously captured root is unchanged.
- A reader's write to the root (`cfg.name = "x"`) is visible to every other reader and to the next merge, because `previous` is that object. It is not visible to the author's own `config` object.
- A write through a child (`cfg.models.push(m)`, `cfg.oauth.name = "y"`) is visible to the author, because the child is the author's object. An author's later write to its own child is visible to readers; an author's later write to its root is not.
- Getter-valued properties on the author's root are read once, during the merge. Getters on children stay live.
- Functions retain identity and their receiver at the call site. `provider-composer.ts:500-501` calls `extension.streamSimple(model, context, options)` with `this` equal to the effective root. `:522-527` calls `extension.refreshModels(context)` with the same receiver. `:281-292` calls `config.login(...)`, `config.refreshToken(credential, signal)` and `config.getApiKey(credential)` with `this` equal to the author's `oauth` object. `:478-479` calls `extension.oauth.modifyModels(models, credential)` with the same `oauth` receiver.
- Sparse arrays, cycles, class instances, symbols and non-JSON values are carried by reference; nothing is serialized.

### 1.2 Provider surface

- `ai/src/models.ts:99-156` defines `Provider`: `id`, `name`, optional `baseUrl`/`headers`, `auth`, synchronous `getModels()`, optional `refreshModels(context)`, synchronous optional `filterModels(models, credential)`, `stream`, `streamSimple`, optional `fetchDeferred` and `cancelDeferred`. Stream methods return an Event Stream synchronously.
- `ai/src/auth/types.ts:175-272` defines `ApiKeyAuth` (`name`, optional `login`, optional `check`, `resolve`) and `OAuthAuth` (`name`, optional `isSubscription`, optional `loginLabel`, `login`, `refresh(credential, signal)`, `toAuth(credential)`).

### 1.3 Credentials

- `ai/src/auth/types.ts:23-33`: `OAuthCredentials` is `{ refresh: string; access: string; expires: number; [key: string]: unknown }`. `expires` is a JavaScript number: it may be fractional. Every other key is provider-owned metadata and is preserved.
- `coding-agent/src/core/auth-storage.ts:216,366,501` reads `auth.json` with `JSON.parse`; `:358,467,479` writes it with `JSON.stringify(data, null, 2)`. Fractional expiry, unknown keys, `null` values and explicit empty values round-trip. `undefined` members are dropped.
- Expiry comparisons use JavaScript relational semantics on whatever value was stored:
  - `ai/src/models.ts:468,473`: `Date.now() < stored.expires` (refresh when false);
  - `ai/src/auth/resolve.ts:136-170`: `Date.now() + minimumValidityMs >= credential.expires` (refresh when true), and a post-refresh check when the caller set `minOAuthValidityMs`.
  An absent value is `undefined` (NaN), so both comparisons are false: `models.ts` refreshes and `resolve.ts` does not. `null` compares as 0. A numeric string compares numerically.
- `provider-composer.ts:292` refresh spreads the extension's returned object and adds `type: "oauth"`; every returned key survives.

### 1.4 Signals

- `ai/src/auth/resolve.ts:149-153` passes `AbortSignal.any([signal, AbortSignal.timeout(15_000)])` to `oauth.refresh`. The signal object remains valid after `refresh` returns; a retained reference later observes the timeout or caller abort (`aborted`, `reason`, `abort` listeners, `throwIfAborted`).
- `provider-composer.ts:288` passes the login interaction signal as `callbacks.signal`; `ai/src/auth/types.ts:120-164` gives each prompt its own `signal` and the interaction a required `signal`. The same retention rule applies.

## 2. Current PiG state

### 2.1 On base2

- Registered native Providers have callable cross-process handles in all four SDKs. Captured handles survive unregister and fail after owner disconnect (`TestProviderObjectsAcrossSDKs`, `TestNodeRemoteProviderObjectCarrier`, `TestProviderObjectReferenceLifetime`).
- Go, Rust and Python `getProvider` raise `builtin/composed Provider object carrier is unavailable (D78)` for builtin/composed ids (`extensions/sdk/provider_proxy.go:46-60`, `extensions/sdk-rs/src/context.rs:421-424`, `extensions/sdk-py/pig_sdk/__init__.py:710-716`).
- Node `getRegisteredProviderConfig` returns the local merged object for a same-runtime author and the replicated JSON `registryState.registered[].config` for everyone else (`runtime-node/runtime.mjs:394-397`). The local path merges before validation and then strips `streamSimple` and OAuth closures into side tables (`runtime.mjs:2328-2371`), so a same-cell caller in another Runtime of the cell cannot see the original functions.
- The host OAuth wire truncates and drops metadata: `subprocess/protocol.go:350-356` `OAuthCredentialsWire.Expires int64`, and `oauth_bridge.go:231-235` copies only six named fields. `ai.OAuthCredentials.Expires` and `ai.Credential.Expires` are `int64` (`ai/oauth_types.go:15`, `ai/auth.go:88,111`). A Pi-written `auth.json` with a fractional `expires` fails to decode: `json.Unmarshal` of `{"type":"oauth","refresh":"r","access":"a","expires":1.5}` into `ai.Credential` returns `cannot unmarshal number 1.5 into Go struct field plain.expires of type int64` (probed on base2). The Go SDK `OAuthCredentials.Expires` is `int64` (`extensions/sdk/oauth.go:37`); Rust `pub expires: i64` (`extensions/sdk-rs/src/oauth.rs:29`); Python `expires: int` with `int(...)` coercion (`extensions/sdk-py/pig_sdk/__init__.py:1465,1486`). None of the native SDKs keeps unknown keys.
- Expiry comparisons use integer milliseconds: `ai/models_runtime_refresh.go:154,161`, `ai/oauth_registry.go:171`, `ai/githubcopilot.go:841`, `subprocess/native_provider_auth.go:135`.

### 2.2 Reusable checkpoints (not integrated)

All branch from `fbcc3a7d5`, which is an ancestor of base2; base2 has 745 later commits, including the `tests/`→`test/` and `parity/`→`test/parity/` moves. `git cherry` confirms none of their commits are on base2.

| Checkpoint | Branch | Content to reuse |
|---|---|---|
| `f91f6bed2` | `fin-d78-carriers` | Raw builtin backend through native SDK carriers (`9e2aa8810`), callable configuration fields and getters, legacy configuration callbacks through ModelRuntime, original OAuth and independent Provider signals, canonical registration cleanup (`c032eac65`), ordered signal control (`f91f6bed2`), `internal/jsdate`, lazy streams, builtin OAuth/API-key login, Pi oracles under `test/parity/probes/`. About 26k changed lines, of which the interface inventory and oracle fixtures are generated. |
| `d9a5fda83` | `d78-go` | Go transmission-aware callback ownership across uncertain registration results; Go reader facade `8a3e1d4d5`. |
| `21949ff4b` | `d78-python` | Python retained callbacks after registration errors; `3d1cc3116` stream ownership before remote invocation. |
| `4d2e17a83` | `d78-node` | Node merge of the shared cleanup and signal ordering. Its same-process accepted-root work is not qualified. |
| `6bb165694` | `d78-rust` | Rust original OAuth, credential data, reader draft `121ba9df7`. Not based on `f91f6bed2`. |

`git merge-tree` against base2 reports 132–162 conflicting paths per branch. Most are directory-move location conflicts for new files; the substantive conflicts are in `runtime.mjs`, `host.go`, `host_calls.go`, the SDK entry files, `ai/auth_providers.go`, `internal/codingagent/model_registry.go`, docs and generated inventories.

The Go and Rust "live configuration readers" (`8a3e1d4d5`, `121ba9df7`) are facades over a lease. They do not implement author-held aliases, accessor/receiver export, sparse/cyclic ownership, first-export acknowledgement or distributed cycle collection. The plan does not integrate them as the reference mechanism. Section 3.4 uses the mechanism from lane gap-xproc.

### 2.3 Neighbouring lanes

- `team/smc1/x-registerprovider` (`a04cb4a04`, READY) updates the available-model snapshot on registration and `fx-provider` (`ae1fb8832`, BLOCKED) covers Pi's unawaited offline refresh. The "implicit refresh" obligation is consumed from those lanes and re-qualified here through the SDK path. This lane does not write a second scheduler.
- `team/smc1/gap-xproc` owns the single cross-process reference mechanism (D83 first, then D73). At the time of writing it has not pushed a PLAN. This lane states its consumer requirements in section 3.4 and does not build a second mechanism.

## 3. Design

### 3.1 Forward-port the checkpoints (W0)

Merge, in order, `fin-d78-carriers`, `d78-go`, `d78-python`, `d78-node` and `d78-rust` onto base2 as ordinary merge commits. Directory-move conflicts accept the moved location. For substantive conflicts, base2 behavior wins unless the checkpoint change is its stated fix; each resolution keeps both sides' tests. Generated files (`pig-go.json`, recommendations, coverage, normalization inventory, SDK `go.sum` lines) take base2's version during the merge and are regenerated only with `make generate` in a separate commit. Scenario numbers that collide on the same path are renumbered to the next free integer; the TOML body is unchanged.

After each merge, run the checkpoint's own named tests plus the base2 suites they touch. A checkpoint test that fails on the merged tree is a merge defect, not a test to relax.

### 3.2 Canonical credential expiry and metadata (W1, independent of gap-xproc)

Representation: the canonical value of `expires` is the original JSON value, not an integer.

- `ai.OAuthCredentials` and `ai.Credential` gain an unexported exact expiry (`raw json.RawMessage`, presence) set by `UnmarshalJSON` and by `SetExpires(float64)`. The exported `Expires int64` stays as a deprecated integer projection (see 4.1). Marshaling writes the exact value when it is present and consistent with `Expires` (`Expires == trunc(ToNumber(raw))`, or both absent); otherwise `Expires` wins, because the caller assigned it. This prevents a stale fractional value from overriding a caller's write.
- `ai.JSNumberCompare` implements ECMAScript `ToNumber` on a JSON value (absent → NaN, `null` → 0, booleans, strings via `StringToNumber`, arrays via `join`, objects → NaN) and the `<`/`>=` relational results including NaN. `models_runtime_refresh.go`, `oauth_registry.go`, `githubcopilot.go` and `native_provider_auth.go` call `(OAuthCredentials).ExpiresBefore(nowMs)`/`ExpiresSoon(nowMs, minMs)` helpers that mirror `models.ts:468,473` and `resolve.ts:136` exactly. `Date.now()` is an integer millisecond, so `nowMs` stays `int64`.
- Pi `JSON.stringify` writes numbers with ECMAScript `Number::toString`. Go `encoding/json` float formatting follows the same algorithm except for `-0`, which JavaScript writes as `0`; the marshaller maps `-0` to `0`. Integers above 2^53 keep their original literal only when read from JSON; arithmetic results use float64 as JavaScript does.
- Host wire: `OAuthCredentialsWire` carries the full JSON object (`json.RawMessage` fields plus named projections) so every key and the exact `expires` cross between SDKs and the host. `oauth_bridge.go` converts through `ai.OAuthCredentials.UnmarshalJSON`/`MarshalJSON` rather than field copies.
- Go SDK: same unexported-exact/deprecated-projection pattern as `ai`, plus `Extra map[string]json.RawMessage` (additive field). Python: `expires: float` (an `int` remains valid), plus an `extra: dict[str, Any]` preserving unknown keys and presence; `from_wire` stops calling `int()`. Rust: add `expires_exact: Option<serde_json::Number>`-backed accessors and `#[serde(flatten)] extra: Map<String, Value>`; keep `pub expires: i64` with the same consistency rule. Node already carries JavaScript numbers; `oauthCredsToWire`/`oauthCredsFromWire` (`runtime.mjs`) stop projecting named fields and send the object.
- `extensions/sdk-ts` needs no change: it re-exports Pi's declarations.

Failure modes: a non-object credential from an SDK callback is an error on that operation (Pi would throw on `{ ...value, type }` only for `null`/`undefined`; a primitive spreads to `{}`). The plan ports that exact rule: `null`/`undefined` → TypeError-equivalent error; a primitive → `{ type: "oauth" }`.

### 3.3 Registration ownership, validation order and rollback (W2)

Pi validates the incoming config before touching state and then merges into a fresh root (1.1). The host becomes the single authority for the effective registration:

- The host stores, per provider id, the effective root as an ordered list of `(key, value, owner)` entries, where `value` is JSON data or a reference owned by one extension connection. Re-registration by any owner copies the previous entries and overwrites the keys whose incoming value is not `undefined`. This is mixed-owner composition: a `streamSimple` supplied by extension A survives extension B's partial re-registration and keeps A as its owner.
- Validation runs on the incoming config alone before the merge (`TestInvalidLegacyRegistrationPreservesCurrentProvider` from the checkpoint). A rejected registration releases only the references that the rejected message introduced; references already stored stay leased (`c032eac65`, `d9a5fda83`, `21949ff4b`).
- A registration whose transmission result is unknown (write failure, cancellation after send) keeps its callbacks until the host acknowledges publication or rejection (`d9a5fda83`, `21949ff4b`). The Node and Rust SDKs receive the same rule.
- Unregister by any owner removes the whole effective root and releases its references. Owner disconnect removes only entries whose provider root that owner last replaced; references to a disconnected owner fail at call time with the existing owner-gone error, as captured functions do in 2.1.
- Ordering: the SDK's `registerProvider` call is synchronous in Pi. The SDK sends the registration and blocks the extension's call until the host acknowledges acceptance or returns the validation error, using the existing synchronous host-call path (`Runtime.callSync` in Node; blocking calls in Go/Rust/Python). The pre-bind queue keeps `loader.ts` ordering and reports errors as `register_provider` like `runner.ts:443-459`.

### 3.4 Foreign configuration identity (W3, depends on gap-xproc)

Required from the gap-xproc mechanism (to be reconciled with its published API; no second mechanism is built here):

1. An owner-side export table: `export(value) → refId`, stable per `(connection, object)` for the object's lifetime, so repeated exports preserve identity.
2. First-export acknowledgement: an owner keeps a strong pin on an exported object until the host acknowledges that the reference is recorded, so an in-flight reference cannot be collected.
3. Reader proxies (JavaScript `Proxy` in Node; handle types in Go/Rust/Python) whose `get`/`set`/`has`/`deleteProperty`/`ownKeys`/`getOwnPropertyDescriptor`/`defineProperty`/`apply` traps execute synchronously in the owner through the host call path. `apply` carries the receiver as a reference, so `this` in the owner is the original receiver object.
4. Leases released by each SDK's collector (`FinalizationRegistry`, Python `weakref.finalize`, Go `runtime.AddCleanup`, Rust `Drop`), plus distributed cycle collection for owner→reader→owner cycles.
5. Reentrant routing: a trap executing in the owner may call back into the reader's process; the host routes nested requests by call-chain id so neither process deadlocks.
6. Connection loss: operations on a reference to a closed owner throw a defined owner-gone error; the host releases every lease that connection held.

Provider use of that mechanism:

- The effective root (3.3) is host-owned data whose values are either JSON primitives or references to author objects. `getRegisteredProviderConfig` in a foreign process returns one proxy per effective-root generation, so repeated calls are `===` and a re-registration yields a new proxy. Reader writes to the root update the host's entry list (visible to all readers and to the next merge). Reads of children resolve to proxies of the author's original objects.
- The author process stops stripping functions from its registration (`runtime.mjs:2355-2371`); it exports the original `config` values. Getters on the author's root are evaluated once, in property order, during the author's own `Object.entries` copy, as Pi does.
- Receivers: when the composed Provider calls `streamSimple`/`refreshModels`, the host sends `apply` with `this` bound to the effective-root reference; OAuth methods use the `oauth` child reference.
- Same-process: when reader and author share a Node cell, the cell uses the real effective root object, not a proxy (D78 scope sentence: same-process must stay Pi-exact).
- Go/Rust/Python authors: the SDK exports a live accessor over the author's value (Go pointer to the registered struct, Python object, Rust `Arc<RwLock<_>>`), so later author writes to children are visible to readers. A native reader receives typed handles whose reads call the owner.

### 3.5 Builtin/composed raw `getProvider` in native SDKs (W4)

Reuse `9e2aa8810` and `d0e892269`: the host retains a raw Provider object per id and native SDKs call it through `provider.get`. After W0 this removes the three native-SDK markers once every `Provider` member in 1.2 works for builtin, models.json-composed and extension-composed ids in isolated, packed and fused placement.

### 3.6 Retained post-callback signal lifetime (W5)

A signal passed to an SDK callback (`refresh(credential, signal)`, login `signal`, prompt `signal`) is a host-owned abort source. The SDK signal object holds a lease on the host source instead of being released when the callback returns. The host keeps forwarding abort (with the Pi reason: `TimeoutError` for the 15 s refresh timeout, the caller's reason otherwise) until the SDK collector releases the lease or the connection closes. Until gap-xproc lands, the lease reuses the existing callback lease table from `f91f6bed2`; after it lands, the signal becomes an ordinary exported host object.

### 3.7 Lifetime, reentrancy and platforms

- Every lease has exactly one owner and one release path (collector, explicit unregister, connection close). Connection close releases all leases held by and owned by that connection; the host joins pending reverse calls before reporting shutdown.
- No blocking IPC runs on the TUI input/render loop. Synchronous reads happen on the extension's worker; host handlers never call back into the same Node process while it is blocked in `callSync`, except through gap-xproc reentrant routing.
- Linux/macOS use Unix sockets; Windows uses the listener in `extension_listener_windows.go`. Collector timing differs by runtime, so lifetime tests force collection (`--expose-gc` in Node, `gc.collect()` in Python, `runtime.GC` plus cleanup wait in Go, scope end in Rust) rather than sleeping. Windows vet and the Windows fixtures from `e071a5658` are run for every touched package.

## 4. Public API impact and migration

### 4.1 Expiry fields

Changing `Expires int64` to `float64` in `ai`, `extensions/sdk` or the Rust SDK is a source-breaking change. W1 keeps the existing fields and adds:

- Go (`ai`, `extensions/sdk`): `ExpiresMillis`, `HasExpires`, `SetExpiresMillis`, `ClearExpires`, and in `ai` the conversions `Credential.OAuthCredentials` and `CredentialFromOAuth`. The exact value is unexported state that applies only while `Expires` still equals its truncated projection, so a caller who writes `Expires` is never overridden by an older fractional value. `Expires` is documented as the projection rather than marked `Deprecated:`, because staticcheck would then flag every existing integer assignment in the repository and in extensions.
- Rust: `expires_millis`, `has_expires`, `set_expires_millis`, `clear_expires`; a new `extra` map holds provider-owned keys and, when `expires` cannot represent the value, the exact `expires` under the same consistency rule; a new `expires_absent` flag defaults to present. The two new public fields require `..Default::default()` in exhaustive struct literals. The crate is 0.1.0 and its own fixtures already use that form.
- Python: `expires` holds the exact value (`int` values remain valid), plus `extra`, `has_expires()` and `clear_expires()`. This is not source-breaking.
- Host: `subprocess.OAuthCredentialsWire` becomes an alias of `ai.OAuthCredentials`, so field access and composite literals keep compiling.

If the owner prefers a direct type change (Pi naming stays `expires`; callers passing `int64` would need a conversion), that is a breaking change requiring approval.

### 4.2 Other additions

- Go SDK `OAuthCredentials.Extra`, Python `extra`, Rust `extra` are additive.
- Native `ModelRegistry.GetProvider` stops returning the D78 error for builtin/composed ids. Callers that matched that error string lose the error; this is the documented closure.
- `GetRegisteredProviderConfig` in native SDKs returns a reference type after W3. The current snapshot-returning signature is kept as a deprecated alias that documents it is a snapshot; the new method name mirrors Pi (`getRegisteredProviderConfig`) only in Node, where the existing function changes behavior to the Pi contract.

## 5. Work breakdown

| Step | Content | Depends on |
|---|---|---|
| W0a | Merge `fin-d78-carriers` onto base2; resolve; run its tests. | — |
| W0b | Merge `d78-go`, `d78-python`, `d78-node`, `d78-rust`; resolve; run their tests. | W0a |
| W0c | `make generate`; separate commit. | W0b |
| W1 | Canonical expiry and metadata in `ai`, host wire, Go/Python/Rust SDKs, Node wire. Pi oracle for comparisons and JSON round-trip. | — (rebased onto W0 when it lands) |
| W2 | Host-owned effective registration with mixed-owner composition, validation-first rollback, synchronous acknowledgement. | W0 |
| W3 | Foreign configuration references on the gap-xproc mechanism. | gap-xproc API |
| W4 | Native builtin/composed `getProvider`; remove the three native markers when the full surface passes. | W0 |
| W5 | Retained signal leases. | W0; final form on gap-xproc |
| W6 | Implicit refresh: consume x-registerprovider/fx-provider; add SDK-path regression. | those lanes |
| W7 | Evidence, docs (`docs/extension-api-parity.md`, `docs/site/docs/providers.md`, pigdocs mirror), D78 record narrowing or removal. | all |

## 6. Test and evidence plan

No existing test, comparator, run count or normalization is weakened.

- Pi oracles: extend `test/parity/probes/` with a probe run against installed Pi 0.87.1 that records, for a list of `expires` JSON values (`1.5`, `1e21`, `-0`, absent, `null`, `"123"`, `[5]`, `{}`, `true`, `2**53+1`): `JSON.stringify` output after `auth-storage` round-trip, `models.ts:468` refresh decision and `resolve.ts:136` refresh decision at a fixed `Date.now()`. `TestCredentialExpiryMatchesPi` compares every row. It is red on base2 (decode failure for `1.5`, wrong decisions for absent/null).
- Metadata: `TestOAuthCredentialMetadataRoundTripAcrossSDKs` sends a refresh result with nested unknown keys, `null`, empty string and fractional expiry from each SDK owner (Go/Rust/Python/Node, isolated and packed, fused Go) and asserts the exact persisted `auth.json` object. Red on base2 (wire drops keys).
- Registration: `TestMixedOwnerRegistrationComposesAndRollsBack` (A registers with `streamSimple`, B re-registers `name` only, stream still reaches A with `this` equal to the effective root; B's invalid registration leaves the root and releases only B's new references). Differential scenario `extensions-runtime/<n>-mixed-owner-provider-config` with a Pi pair (two Node extensions) versus PiG (Python owner, Node second owner), strict `output_equal` on an artifact, `runs = 3`.
- Identity (W3): Pi-derived scenario asserting `===` across calls, reader root write visibility, child write visibility both ways, author root write invisibility, getter evaluation count, receiver identity for `streamSimple`/`refreshModels`/OAuth methods, sparse array `in` checks and a cycle. Pi runs both extensions in one process; PiG runs them in separate Node processes (strict isolation or separate cells), so a local shortcut cannot pass. Mutation proof: a compiling mutation that returns a JSON snapshot must fail it.
- Lifetime: forced-collection tests per SDK for config proxies and retained signals; connection-close release; owner-gone errors; a leak test that registers/unregisters 10,000 times and asserts the host lease table returns to its baseline.
- Signals: `TestRetainedRefreshSignalObservesTimeout` in each SDK, using an injectable clock for the 15 s timeout (no sleeps), red on base2.
- Placement: every row runs isolated, packed and fused (Go) through `TestProviderObjectsAcrossSDKs`-style matrices.
- Gates: `go build ./...`, `go vet ./...` plus Windows vet on touched packages, targeted `-race -count=3`, SDK suites (`make test-sdk-*`), `make lint`, `make test-porting-release`, the strict extensions-runtime scenarios touched (`31`, `52`, `53-node-provider-object-carrier`, `63`, the imported `55`–`68` set and the new ones), each at declared durability in a private tmux server that is killed afterwards.
- Performance: `BenchmarkRemoteProviderObjectRoundTrip` and a new configuration-proxy read benchmark with CPU/allocation profiles, before and after.

## 7. Conditions for removing D78

Subscopes are removed independently:

1. Native builtin/composed `getProvider`: remove the Go/Rust/Python markers when W4 passes the complete 1.2 surface for builtin, models.json and extension-composed ids in every placement.
2. Credential expiry/metadata: not part of the D78 text as a divergence, but listed as an explicit obligation; W1 closes it with the oracle and the cross-SDK round-trip.
3. Foreign configuration identity: remove the `runtime.mjs` marker and the record when W3 passes the identity scenario and lifetime/cycle tests for Node and native readers, with isolated/packed/fused caller, cancellation, replacement, cleanup and resource evidence (the record's remove-when text).

If gap-xproc does not deliver before the cut, subscopes 1 and 2 still land, and the record is narrowed to foreign configuration identity with the current markers.

## 8. Progress

- W1 landed in `fix(auth): keep OAuth credentials as Pi's exact token object across host and SDKs`: canonical fractional/absent/non-number expiry and provider-owned keys in `ai`, the host wire, Go/Rust/Python/Node SDKs and builtin flows, with the Pi oracle `ai/testdata/credential-expiry.json`.
- W2 (same-process part) landed in `fix(extensions): share Pi's effective Provider configuration root within a Node process`: validation before merge, one cell-shared effective root, `streamSimple` receiver, and latest-registrant cleanup ownership in the host.
- gap-xproc published its mechanism (`xref`, plan section 4 on its branch): realms per process, `$x` values, proxy traps executed in the owner, host lease holds/deliveries, release/unpin, owner-exit failure. W3 builds on it: the author realm sends the effective root as an `xref` value with its registration; the host holds that frame until the registration is superseded or its owner exits, and counts one delivery per registry-state transmission; readers decode the reference once per delivery and return the canonical proxy. A cross-process partial re-registration then merges over the foreign root proxy, as Pi merges over the stored root.
- W3 (Node realms) landed after merging gap-xproc: the author sends `configRef` with `registerProvider` (or `provider.configRef` after the handshake for loading-time registrations); the Host holds it while current (`providerConfigRefs`), answers a reader's synchronous `provider.config` with one counted delivery, drops it on unregister, native replacement or owner teardown, and notifies superseded owners in other processes (`provider_superseded`). Native SDK (Go/Rust/Python) readers and authors remain snapshots until those SDKs implement xref handles.
- W5 (Node): OAuth callbacks receive Pi's signals (`TestNodeOAuthSignals`).

## 9. Status at READY

Closed with evidence: canonical fractional/absent/non-number credential expiry and provider-owned keys (all four SDKs, host, auth storage, builtin flows); same-process effective configuration root, validation-before-merge, `streamSimple` receiver and latest-registrant cleanup ownership; Node-to-Node foreign configuration identity through xref, including lifetime and superseded-owner handling; Node OAuth signals.

Not closed, still recorded in D78: builtin/composed raw `getProvider` for Go/Rust/Python (W4) and the forward-port of the native-SDK checkpoints (W0), so native SDK authors and readers keep snapshot configuration and lack signals; accessor/receiver export for native handles; sparse/cyclic ownership beyond what xref proxies provide; distributed cycle collection (shared xref residual); implicit registration refresh (owned by the x-registerprovider and fx-provider lanes); native mixed-owner acceptance/rollback. The record therefore stays.
