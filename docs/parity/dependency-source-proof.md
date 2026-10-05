# Upstream dependency source proof

Pi-owned source can inherit observable defaults from an exactly pinned dependency. `test/parity/dependency-sources.json` records reviewed dependency source snapshots for that case. It does not grant a divergence or a general allowance for the dependency.

The divergence guard accepts an exact `upstream: node_modules/<package>/<file>:<symbol>` reference only after it validates the snapshot. It requires the package version to match an exact dependency pin in the current upstream package manifest. It requires the snapshot SHA-256 to match the recorded digest. It rejects missing files, duplicate references, ranges, traversal, malformed records and stale pins or digests. It then checks the named symbol and the literal value with the same rules as a Pi-owned source reference. Numeric comparison recognizes equivalent integer exponent notation, including Undici's `10e3` spelling of 10000.

CI reads the reviewed snapshot offline. It does not trust an ambient node_modules installation or fetch mutable source during a gate run. The recorded npm archive integrity identifies the archive used for capture; capture verifies its SHA-512 before extracting source. Changing the source and its digest requires code review, just as changing a pinned mirror or a parity oracle does. These syntactic checks do not replace behavioral tests.

## Current dependency

| Property | Proof |
|---|---|
| Upstream owner | `.upstream/current/packages/coding-agent/package.json` |
| Exact dependency | `undici` 8.10.2 |
| Source | `test/parity/dependency-sources/undici/8.10.2/lib/core/connect.js` |
| License | `test/parity/dependency-sources/undici/8.10.2/LICENSE` |
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

## Capture and review

1. Read the exact dependency version from the current upstream manifest.
2. Fetch that exact package's npm metadata and archive into an ephemeral directory.
3. Verify the archive against `dist.integrity` before opening it.
4. Extract only the reviewed source files and the license without modifying their contents.
5. Update the snapshot paths, source digests and archive integrity in `test/parity/dependency-sources.json`.
6. Review the default, completion event and error/cancellation behavior in the new source.
7. Run the guard tests, transport tests and provider scenario before accepting the update.

The capture for 8.10.2 uses:

```sh
work=$(mktemp -d)
export work
curl -fsS https://registry.npmjs.org/undici/8.10.2 -o "$work/package.json"
curl -fsS https://registry.npmjs.org/undici/-/undici-8.10.2.tgz -o "$work/package.tgz"
python3 - <<'PY'
import base64, hashlib, json, os, pathlib, tarfile
work = pathlib.Path(os.environ['work'])
metadata = json.loads((work / 'package.json').read_text())
archive = (work / 'package.tgz').read_bytes()
assert metadata['version'] == '8.10.2'
integrity = 'sha512-' + base64.b64encode(hashlib.sha512(archive).digest()).decode()
assert integrity == metadata['dist']['integrity']
with tarfile.open(work / 'package.tgz') as package:
    for name in ['lib/core/connect.js', 'LICENSE']:
        source = package.extractfile('package/' + name).read()
        target = pathlib.Path('test/parity/dependency-sources/undici/8.10.2') / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(source)
        print(name, hashlib.sha256(source).hexdigest())
print(integrity)
PY
```

Keep the capture output and behavioral probe output with the owning lane's evidence. Do not edit `.upstream/current` to make a dependency default look like Pi-owned source.
