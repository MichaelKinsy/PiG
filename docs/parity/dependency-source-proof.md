# Upstream dependency source proof

Pi-owned source can inherit observable defaults from an exactly pinned dependency. `test/parity/dependency-sources.json` records reviewed dependency source snapshots for that case. It does not grant a divergence or a general allowance for the dependency.

The divergence guard accepts an exact `upstream: node_modules/<package>/<file>:<symbol>` reference only after it validates the snapshot. It requires the package version to match the exact dependency pin in force: the one in the current upstream package manifest, or, where a `dependency(...)` entry of `automation/gen/pi-post-release-backports.mjs` replaces that pin for the same Pi package (the entry's `from` still equals the manifest's pin), the entry's version, whose archive integrity the record must equal. A snapshot of any other version is rejected. It requires the snapshot SHA-256 to match the recorded digest. It rejects missing files, duplicate references, ranges, traversal, malformed records and stale pins or digests. It then checks the named symbol and the literal value with the same rules as a Pi-owned source reference. Numeric comparison recognizes equivalent integer exponent notation, including Undici's `10e3` spelling of 10000.

CI reads the reviewed snapshot offline. It does not trust an ambient node_modules installation or fetch mutable source during a gate run. The recorded npm archive integrity identifies the archive used for capture; capture verifies its SHA-512 before extracting source. Changing the source and its digest requires code review, just as changing a pinned mirror or a parity oracle does. These syntactic checks do not replace behavioral tests.

## Current dependency

| Property | Proof |
|---|---|
| Upstream owner | `.upstream/current/packages/coding-agent/package.json` (undici 8.12.3), whose pin Pi efbcd5adb moves to 8.12.4: the post-release backports script carries that as a `dependency(...)` entry |
| Exact dependency | `undici` 8.12.4, the pin in force: the guard takes the carried version while the mirror still pins the version the entry replaces (8.12.3) and the record's integrity equals the entry's |
| Source | `test/parity/dependency-sources/undici/8.12.4/lib/core/connect.js` |
| License | `test/parity/dependency-sources/undici/8.12.4/LICENSE` |
| Symbol | `buildConnector` |
| Rule | Default connection timeout is 10000ms; the connector clears the timer on `secureConnect` for TLS and `connect` for TCP |
| Literal consumer | `ai/http_transport.go:defaultHTTPConnectTimeout` |
| Behavioral proof | `TestHTTPConnectBudgetIncludesTCPAndTLS`, `TestHTTPIdleTimeoutBeginsAfterTLS`, `TestHTTPIdleTimeoutStillAppliesAfterTLS`, scenario `24-http-connect-budget` |

The provider scenario executes the actual pinned Pi dispatcher and provider. Its wrapper observes the selected connector timeout without modifying the timer. Both implementations connect through a loopback relay that delays TLS longer than the configured HTTP idle timeout.

## Anthropic SDK credential limits

| Property | Proof |
|---|---|
| Upstream owner | `.upstream/current/packages/ai/package.json` |
| Exact dependency | `@anthropic-ai/sdk` 0.129.0 |
| Source | `test/parity/dependency-sources/@anthropic-ai/sdk/0.129.0/lib/credentials/types.mjs` and `oidc-federation.mjs` |
| License | `test/parity/dependency-sources/@anthropic-ai/sdk/0.129.0/LICENSE` |
| Symbols | `MAX_TOKEN_RESPONSE_BYTES`, `MAX_ERROR_BODY_CHARS`, `ADVISORY_REFRESH_BACKOFF_IN_SECONDS`, and the 16 KiB identity-token check in `oidc-federation.mjs` |
| Rule | The SDK's workload identity federation reads at most 1 MiB of a token response, quotes at most 2000 characters of an error body, backs off 5 seconds after a failed advisory refresh, and rejects an identity token longer than 16 * 1024 characters |
| Literal consumer | `ai/anthropic_federation.go` (`anthropicMaxTokenResponseBytes`, `anthropicMaxErrorBodyChars`, `anthropicAdvisoryBackoffSeconds`, `anthropicMaxIdentityTokenChars`) |
| Behavioral proof | `ai/anthropic_federation_sdk_upstream_test.go`, `ai/anthropic_token_cache_test.go` |

The capture follows the same steps as for undici, with `lib/credentials/types.mjs`, `lib/credentials/oidc-federation.mjs` and `LICENSE` extracted from the 0.129.0 archive (unchanged from 0.124.0) after its `dist.integrity` matched.

## TypeBox error limit

| Property | Proof |
|---|---|
| Upstream owner | `.upstream/current/packages/coding-agent/package.json` |
| Exact dependency | `typebox` 1.3.27 |
| Source | `test/parity/dependency-sources/typebox/1.3.27/build/system/settings/settings.mjs` |
| License | `test/parity/dependency-sources/typebox/1.3.27/LICENSE` |
| Symbol | `maxErrors` |
| Rule | TypeBox stops collecting validation errors after 8, so Pi's models.json error text lists at most 8 |
| Literal consumer | `internal/codingagent/configschema/validate.go` (`maxSchemaErrors`) |

## Smithy retry constants

| Property | Proof |
|---|---|
| Upstream owner | `.upstream/current/package-lock.json` (`node_modules/@smithy/core`), a transitive dependency of `@aws-sdk/client-bedrock-runtime` 3.1127.0 that Pi's build bundles into its Bedrock client |
| Exact dependency | `@smithy/core` 3.33.3, with the lockfile's archive integrity |
| Source | `test/parity/dependency-sources/@smithy/core/3.33.3/dist-cjs/submodules/retry/index.js` |
| License | `test/parity/dependency-sources/@smithy/core/3.33.3/LICENSE` |
| Symbols | `DEFAULT_RETRY_DELAY_BASE`, `THROTTLING_RETRY_DELAY_BASE`, `MAXIMUM_RETRY_DELAY`, `THROTTLING_ERROR_CODES`, `refreshRetryTokenForRetry` |
| Rule | The SDK's standard retry waits floor(random() * min(base * 2^attempts, 20 * 1000)) ms, with base 500 for a throttling error and 100 otherwise, and raises it to a retry-after hint by at most 5_000 ms |
| Literal consumer | `ai/providers/bedrock/bedrock_retry_backoff.go` |
| Behavioral proof | `ai/providers/bedrock/bedrock_retry_backoff_test.go` |

A package that no workspace manifest declares is admitted through the root lockfile: the guard requires the record's version and archive integrity to equal the lockfile entry's.

## Node's bundled minimatch

| Property | Proof |
|---|---|
| Upstream owner | Node, not Pi: `fs.globSync` (Pi's `expandPackageGlob`, `package-manager.ts`) runs the minimatch that ships in Node's own `deps/minimatch`, not the `minimatch` that Pi's `package.json` pins |
| Exact release | Node 24.22.2, the release `.node-version` pins and the parity oracle runs; the record cites `.node-version` as its manifest and the guard requires the record's version to equal it |
| Source | `test/parity/dependency-sources/node/24.22.2/deps/minimatch/index.js`, cited as `upstream: node-deps/minimatch/index.js:<symbol>` (the `node-deps/` prefix cannot be mistaken for an npm pin) |
| Integrity | `sha256-` of `node-v24.22.2.tar.gz` from nodejs.org `SHASUMS256.txt`; capture verifies the archive before extracting `deps/minimatch/index.js` |
| Symbols | `maxExtglobRecursion` |
| Rule | Node's matcher parses extglobs with the regular-expression AST of minimatch 10.2.6 and nests them two deep; Pi's own `minimatch()` calls run minimatch 11.0.3, which has no AST |
| Literal consumer | `coding/packagecontent/glob_ast.go` (`globMaxExtglobRecursion`) |

Advancing `.node-version` fails the guard until the snapshot is re-captured from the new release.

## lovely-mermaid renderer limits

| Property | Proof |
|---|---|
| Upstream owner | `.upstream/current/packages/coding-agent/package.json` |
| Exact dependency | `lovely-mermaid` 0.3.3 (archive SHA-256 `b9426cfb1cb7625a5e5ba29d625a39bf786ef424d5d398372b3e623497a802d3`, npm integrity as in `Published npm integrity` of `THIRD_PARTY_NOTICES.md`; git revision `a28f683ef3d9eda3497e5818f4f18dfdc3a87ed9`) |
| Source | `test/parity/dependency-sources/lovely-mermaid/0.3.3/src/graph.ts`, `src/labels.ts` and `src/diagrams/pie.ts` |
| License | `test/parity/dependency-sources/lovely-mermaid/0.3.3/LICENSE` |
| Symbols | `MAX_NODES`, `MAX_EDGES`, `MAX_GROUPS`, `MAX_GROUP_DEPTH`, `MAX_MEMBERS` (graph.ts), `WRAP_WIDTH`, `MAX_LABEL`, `MAX_LINES` (labels.ts) and `BAR_W` (pie.ts) |
| Rule | A diagram renders its prefix once it passes 128 nodes, 512 edges, 24 subgraphs, 6 subgraph levels or 8 members per box; node labels wrap at 24 columns over at most 4 lines, edge labels cut at 28 columns, and a pie bar is 20 columns wide |
| Literal consumer | `internal/mermaid` (`maxNodes`, `maxEdges`, `maxGroups`, `maxGroupDepth`, `maxMembers` in graph.go; `wrapWidth`, `maxLines`, `maxLabel` in labels.go; `pieBarW` in pie.go) |
| Behavioral proof | `TestRenderMatchesUpstream`, `TestParseMatchesUpstream` and the mutated-source tests in `internal/mermaid`, which run the installed lovely-mermaid package over the corpus, including the size-cap cases |

The capture follows the steps below with the three files and `LICENSE` extracted from the 0.3.3 archive after its `dist.integrity` matched the one `package-lock.json` records for Pi's build.

## The oracle's carried dependency

The parity oracle is the pinned Pi plus the Pi commits the release line ports ahead of the pin (`docs/parity/upstream-pin.md`, Carried work). One of them, f72f799a4 (#12103), needs a newer dependency than the pin installs, so the oracle installs that one package at the carried version. This is test-oracle tooling: PiG's product has no `@google/genai`. Its Go body encoder is a port of the SDK's converters, though: `ai/google_wire.go` writes a request body in their member order and names the version it mirrors (`geminiMirroredSDKVersion`, 2.32.0), and `make pin-check` and `TestGoogleWireMirrorsTheInstalledSDK` fail when that is not the version the oracle installs, so an SDK update in Pi is seen as a port obligation.

| Property | Proof |
|---|---|
| Carried for | Pi f72f799a4: the Google adapters pass the caller's `fetch` to the SDK, which takes one (`httpOptions.fetch`) only from 2.32.0 on. Entry `dependency("f72f799a4", "pi-ai", ...)` of `automation/gen/pi-post-release-backports.mjs` |
| Exact dependency | `@google/genai` 2.32.0 in place of the pin's 2.21.0, the exact pin of `packages/ai/package.json` |
| Integrity | `sha512-60c79uXpR0a47bmGqDxbXEBuQUalNmwo5mx5kqb91jIW4FoqpsLSt86DeVhRhtH5+s/16Wqxt3sXzFQn2U1OHQ==`, the entry `node_modules/@google/genai` of Pi's `package-lock.json` and of `packages/coding-agent/install-lock/package-lock.json` at f72f799a4, read from the Pi git (`git show f72f799a4:package-lock.json`), never from the registry |
| Transitive entries | None. At f72f799a4 both locks change that entry's version, archive and integrity and pi-ai's range, and nothing else; the entry's own dependencies (`google-auth-library` ^10.3.0, `p-retry` ^4.6.2, `protobufjs` ^7.5.4, `ws` ^8.18.0) are those of 2.21.0. The entry records them, and the build stops when they differ from the frozen closure's entry or from the manifest of the installed archive |
| Install | `npm ci` of the frozen closure with that one lock entry in place (`oracle.carried_closure`): npm verifies the archive against the integrity, and `check-oracle` compares the installed tree with the same lock. `automation/pin/frozen/<commit>.closure.json.gz` is not rewritten and stays the source of every other entry |
| Digest | No separate one: the stamp's `closure_sha256` is the digest of the lock after the carried entry, the oracle's key is made of it, and the backports digest covers the entry's text. `check-oracle` prints the closure digest, the frozen one and the carried dependency |
| Bundle | The `pi` bin runs Pi's bundle, which contains the SDK: for the one bundler run that makes the oracle's bundle, the source build's 2.21.0 is replaced by a copy of the installed archive and put back |
| Loud failures | An archive that does not match the integrity: `npm ci` fails (EINTEGRITY). A frozen closure that no longer installs 2.21.0 (a re-pin that already holds the commit): the build stops naming the entry and says to delete it, `make pin-check` fails on the same check without a build, and `make pin-bump` classifies the entry *carried* and deletes it with the commit's blocks. A source build that does not resolve one copy of 2.21.0: the build stops |

The oracle carries the commit's behaviour: the SDK, the three compiled adapters (`pi-ai/api/google-generative-ai.js`, `google-shared.js` and `google-vertex.js`, byte for byte what Pi's build of the commit emits) and the `pi` bundle made of both. It deliberately does not carry what authors and inventories derive from the installed package, which follows the pin until the re-pin: `pi-ai/package.json` keeps the range 2.21.0 (so `npm ls` in the oracle reports the installed SDK as invalid) and `google-shared.d.ts` keeps the declaration of `mapStopReasonString`, so `extensions/sdk-ts` and `test/parity/interfaces` do not move; the six source maps of the adapters are not carried either.

## Capture and review

1. Read the exact dependency version from the current upstream manifest.
2. Fetch that exact package's npm metadata and archive into an ephemeral directory.
3. Verify the archive against `dist.integrity` before opening it.
4. Extract only the reviewed source files and the license without modifying their contents.
5. Update the snapshot paths, source digests and archive integrity in `test/parity/dependency-sources.json`.
6. Review the default, completion event and error/cancellation behavior in the new source.
7. Run the guard tests, transport tests and provider scenario before accepting the update.

The capture for 8.12.4 uses (`lib/core/connect.js` and `LICENSE` are byte-identical in 8.12.3, whose archive has SHA-256 `e03fa2ed347527c915026c04810d3b9bcf9893a2176b314aa4c29c945612d18c`; the 8.12.4 archive has `26058cdb2174623666e823e6b44ccfef8d5c1955107f979b8083904b61a0ce6e`):

```sh
work=$(mktemp -d)
export work
curl -fsS https://registry.npmjs.org/undici/8.12.4 -o "$work/package.json"
curl -fsS https://registry.npmjs.org/undici/-/undici-8.12.4.tgz -o "$work/package.tgz"
python3 - <<'PY'
import base64, hashlib, json, os, pathlib, tarfile
work = pathlib.Path(os.environ['work'])
metadata = json.loads((work / 'package.json').read_text())
archive = (work / 'package.tgz').read_bytes()
assert metadata['version'] == '8.12.4'
integrity = 'sha512-' + base64.b64encode(hashlib.sha512(archive).digest()).decode()
assert integrity == metadata['dist']['integrity']
with tarfile.open(work / 'package.tgz') as package:
    for name in ['lib/core/connect.js', 'LICENSE']:
        source = package.extractfile('package/' + name).read()
        target = pathlib.Path('test/parity/dependency-sources/undici/8.12.4') / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(source)
        print(name, hashlib.sha256(source).hexdigest())
print(integrity)
PY
```

Keep the capture output and behavioral probe output with the owning lane's evidence. Do not edit `.upstream/current` to make a dependency default look like Pi-owned source.
