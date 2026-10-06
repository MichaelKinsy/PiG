# Site patch: show the signed Binary of a published Piglet

`piglet-npm-publish-site.patch` is for the PiG-platform repository (pi-in-go.dev). It applies with `git apply` to `main` at `91553f7`. Do not push it from here.

## What it does

`pig piglet publish <name> --to npm` records the signed Binary release of a Piglet in the published `package.json`:

```json
"pig": {
  "piglet": "piglet.yaml",
  "binaries": { "ref": "github:acme/reviewer/reviewer@1.2.3", "signer": "ed25519:<32 hex digits>" }
}
```

The patch makes `scripts/build-npm-catalog.mjs` read that value for the Piglets source and `app/package-catalog.tsx` show `pig piglet pull <ref>` beside `pig piglet add npm:<name>`.

- npm search results do not include `pig`, so the build fetches `https://registry.npmjs.org/<name>/<version>` for each listed Piglet, one request per 250 ms.
- The reference is printed inside a shell command. `pigletBinaries` accepts only `github:<owner>/<repo>[/<piglet>]@<semver>` with PiG's character set, and a signer that is `ed25519:` plus 32 hex digits. Anything else is dropped.
- A Piglet whose `package.json` cannot be read keeps the value from the committed snapshot when its version is unchanged. The build does not fail, and a new version does not inherit an old release.
- The published value is the publisher's claim. The catalog does not verify signatures; `pig piglet pull` does.
- `docs/catalog-sync.md` and the "Publish a Piglet" panel describe the new field and the `--to npm` command.

## Where the field lives

`pig.piglet` stays a string, the path `pig piglet add` reads, because `pig piglet add npm:<package>` decodes `pig.piglet` as a string and a Piglet published with plain `npm publish` and that field keeps working. On 2026-10-05 no npm package carried the `pig-piglet` keyword, and the 25 `@pi-in-go/pigpen-*` packages with `pig-package` are Packages that carry a `pi` manifest and no `pig` field, so neither field affects them. The Binary release is `pig.binaries`, a sibling of `pig.piglet`, not `pig.piglet.binaries`.

## Tests

`test/npm-catalog.test.mjs` gains five tests: the reference grammar (including shell metacharacters and a malformed signer), the pull command and manifest URL, adding releases to Piglet entries only, keeping the saved release for an unreadable manifest of the same version, and the snapshot round trip. `node --test test/npm-catalog.test.mjs` passes 24 tests. The full `node --test test/*.test.mjs` run has the same three failures with and without the patch (they need a PiG checkout and generated data that the working copy lacked).

## Ordering

The cards show the pull command only for Piglets published with a PiG that writes `pig.binaries` (PiG 0.4.2). Older listings are unchanged. The `/docs/latest/publishing` page ships with PiG 0.4.2, so link to it after the docs sync.
