# Publishing your Package or Piglet

PiG lists Packages and Piglets that authors publish to npm, the way Pi's gallery lists Pi packages. Publishing is ordinary npm publishing plus one npm keyword. This page covers both commands, signed Piglet Binaries, how the pi-in-go.dev catalog finds what you publish, and npm authentication, including CI.

## What gets listed

| You publish | npm keyword | Install command shown | Publish with |
|---|---|---|---|
| a Package | `pig-package` | `pig install npm:<name>` | `npm publish`, or `pig package publish --to npm` |
| a Piglet's source | `pig-piglet` | `pig piglet add npm:<name>` | `pig piglet publish --to npm` |

Pi packages carry the keyword `pi-package`. A Package that works in both Pi and PiG may carry both keywords; a package that has several keywords is listed under the most specific one (`pig-piglet`, then `pig-package`, then `pi-package`).

The pi-in-go.dev catalog finds packages by searching npm for these keywords when its site builds each day, so a package appears after the next build. The catalog has no upload step, and a listing is not a review: community listings are unreviewed.

## Publish a Package

A Package is an ordinary npm package. Add the keyword and a description to `package.json`, then publish as you do for any npm package:

```json
{
  "name": "@acme/review",
  "version": "1.2.0",
  "description": "Review helpers for PiG",
  "keywords": ["pig-package"],
  "pi": { "extensions": ["extensions/*"] }
}
```

```bash
npm publish --access public
```

`pig package publish` is optional sugar over that command. It validates the Package, checks that `package.json` has the fields the catalog reads (`name`, `version`, `description`, and the `pig-package` keyword), checks that `name@version` is free on npm, and shows what npm would pack:

```bash
pig package publish ./base-coding --to npm
```

Without `--yes` it is a dry run: it runs `npm publish --dry-run`, prints npm's tarball listing and the exact command, and publishes nothing. With `--yes` it runs `npm publish` in the Package directory, so your `.npmrc` and scripts such as `prepublishOnly` apply as they do for plain `npm publish`. PiG never edits `package.json`; a missing keyword is an error that tells you what to add.

## Publish a Piglet

A Piglet publishes as source. Set `release.version` in the Piglet, then run:

```bash
pig piglet publish ./agents/reviewer.yaml --to npm
```

PiG validates the Piglet as `pig piglet validate` does, writes a temporary npm package, checks the registry, and shows what npm would publish. Add `--yes` to publish:

```bash
pig piglet publish ./agents/reviewer.yaml --to npm --yes --access public
```

The generated package contains `piglet.yaml`, `package.json`, your `README.md` and `LICENSE` beside the Piglet (a short README is written when there is none), and the system prompt file the Piglet names. In `package.json`:

- `name` is the `name` in a `package.json` beside the Piglet, else the Piglet's own name. `--npm-name @acme/reviewer` sets it. Choose a scoped name: a short Piglet name such as `reviewer` is probably taken.
- `version` is `release.version`.
- `keywords` includes `pig-piglet`, with any keywords from your own `package.json`.
- `pig.piglet` is `piglet.yaml`, the path `pig piglet add` reads. `license`, `author`, `repository`, `homepage`, `bugs`, `funding`, and `publishConfig` come from your `package.json` when you have one beside the Piglet. Scripts and dependencies never do.

A remote Piglet cannot name local paths. PiG replaces each Package with `local:` source by `npm:<name>@^<version>`, using the `name` and `version` in that Package's own `package.json`. The authored Piglet:

```yaml
packages:
  review: local:./packages/review
```

The published Piglet:

```yaml
packages:
  review: npm:@acme/review@^1.2.0
```

When that `package.json` sets `publishConfig.registry`, the reference names that registry, for example `npm:@acme/review@^1.2.0?registry=https%3A%2F%2Fnpm.pkg.github.com`, so `pig piglet add` installs the Package from it. The registry must be an HTTPS URL without credentials; otherwise publication stops and asks for `--package-map`.

Publish the Package first. PiG asks npm whether a published version satisfies each `npm:` Package of the published Piglet that names no registry of its own. The dry run lists every missing Package as a warning, and `--yes` refuses to publish a Piglet that nobody could add. When a local Package has no npm name and version, the error says so; publish it first, or give its npm reference yourself:

```bash
pig piglet publish ./agents/reviewer.yaml --to npm --package-map review=npm:@acme/review@^1.2.0
```

`--package-map <alias>=npm:<name>@<range>` can be repeated. PiG refuses what `pig piglet add` would refuse: `extends`, `agentEnv.devContainer`, a local `agentEnv.source`, file-based secrets, and extensions or skills with a `local:` origin. Move a local extension into a Package and publish that Package.

Publishing is safe to repeat. PiG asks npm whether `name@version` exists (`npm view`, on the registry in `publishConfig.registry` when `package.json` names one) and refuses to publish over it. Raise `release.version` and publish again.

### Options

| Option | Meaning |
|---|---|
| `--yes` | Run `npm publish`. Without it the command is a dry run. |
| `--tag <dist-tag>` | npm dist-tag. A prerelease version such as `2.0.0-rc.1` needs one, for example `--tag next`. |
| `--access public\|restricted` | Package access. npm's default applies when omitted, and scoped packages default to restricted. |
| `--otp <code>` | One-time password from your authenticator. |
| `--npm-name <name>` | Published package name (Piglets only). |
| `--package-map <alias>=<ref>` | npm reference for a Package alias (Piglets only). |
| `--binaries github:<owner/repo>` | Record the signed Binary release (Piglets only). |
| `--no-binaries` | Record no signed Binary release (Piglets only). |

PiG runs the `npm` on your `PATH`, or the command in the global `npmCommand` setting.

## Signed Binaries next to the source

The npm package is the Piglet's source. A signed per-platform Binary is a separate artifact that you publish to GitHub Releases:

```bash
pig piglet publish ./agents/reviewer.yaml --to github --repo acme/reviewer --sign-key ./reviewer-signing.key --yes
```

See [Piglet Binaries](piglets.md) for the release layout. When you then publish the source to npm, PiG records the signed release in `package.json`, so a catalog can show how to pull the Binary beside how to add the source:

```json
"pig": {
  "piglet": "piglet.yaml",
  "binaries": { "ref": "github:acme/reviewer/reviewer@1.2.3", "signer": "ed25519:..." }
}
```

PiG takes the release from the publication this machine made with `--to github`. To name a release you published elsewhere, pass `--binaries github:acme/reviewer`: PiG downloads and verifies the signed release index, checks that it is for this Piglet and version, and records the release reference and the signer key id from it. `--no-binaries` records nothing. Anyone can pull the Binary with `pig piglet pull <ref>`, which verifies the signatures and pins the signer.

## Add it back

`pig piglet add npm:<name>` accepts exactly what publication produces. It installs the package, reads `pig.piglet`, validates the Piglet, resolves its Packages from npm, and registers the source with an origin record that holds the exact npm version and integrity:

```bash
pig piglet add npm:@acme/reviewer
pig --piglet reviewer
```

## npm authentication

npm does the authentication. PiG never reads, stores, or forwards an npm token: `npm publish` runs with your environment and your npm configuration.

- **First publish.** Log in with `npm login`. If your account requires two-factor authentication for writes, npm asks for a one-time password or a passkey on each publish, or pass `--otp`. A scoped package needs the scope to be yours, and `--access public` for a public first publish.
- **First publish is manual.** An npm Trusted Publisher can only be added to a package that already exists, so publish the first version from your own machine.
- **Trusted publishing in CI.** After the first publish, open the package's Settings on npmjs.com, add a Trusted Publisher (GitHub Actions) with your repository and workflow file name, and publish from that workflow with no stored token. The workflow needs `id-token: write` and npm 11.5.1 or newer. GitHub Actions then sets `ACTIONS_ID_TOKEN_REQUEST_URL`, and PiG adds `--provenance` to `npm publish` so the package carries a provenance statement. Your `package.json` must name the repository.

```yaml
permissions:
  contents: read
  id-token: write
steps:
  - uses: actions/checkout@v4
  - uses: actions/setup-node@v4
    with: { node-version: 24 }
  - run: pig package publish ./packages/review --to npm --yes
  - run: pig piglet publish ./piglets/reviewer.yaml --to npm --yes
```

Publish the Packages before the Piglet that names them, as above. In the Piglet example, `pig` comes from your install step.

## Additive

Publishing to npm has no Pi counterpart. Pi's package workflow is plain `npm publish` with the `pi-package` keyword, and PiG keeps that working unchanged. `pig package publish --to npm` and `pig piglet publish --to npm` are PiG additions (D18). The PiG repository records them in `docs/additive-features.md`.
