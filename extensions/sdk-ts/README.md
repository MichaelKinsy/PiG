# PiG TypeScript extension declarations

This package adds PiG-specific declarations to the exact TypeScript extension
API from Pi 1.6.0. It contains no runtime implementation.

Install the declarations from this source tree with the pinned Pi package:

```bash
npm install --save-dev --install-links \
  /path/to/PiG/extensions/sdk-ts \
  @earendil-works/pi-coding-agent@1.6.0
```

## What you type-check against

PiG runs Pi at a pinned commit, which can be past the published release the
peer dependency names. `pi-pin.d.ts` holds the declarations of that pinned
build for `@earendil-works/pi-coding-agent`, `pi-ai`, `pi-tui` and
`pi-agent-core` as ambient modules, and `index.d.ts` references it. The
compiler looks an ambient module up before the installed package, so once a
file of your project imports `@michaelkinsy/pig-extension-types`, every import
of those four packages type-checks against the pin. It needs no `tsconfig`
entry and works with `moduleResolution` `Node16` and `Bundler`, with and
without `skipLibCheck`. A project that never imports this package type-checks
against the installed release.

A member the pin has and the published release lacks exists only in PiG: an
extension that uses it does not run on that Pi release. The comment at the top
of `pi-pin.d.ts` lists those members, and `make test-sdk-ts` prints them.

The model catalog is one of those members. `MODELS` in `pi-pin.d.ts` is typed
by the models Pi's catalog generator makes from the recording PiG commits
(`automation/gen/catalog-source`), as a Pi built from source at the pin types
it; the published `@earendil-works/pi-ai` release carries the frozen snapshot
of the day it was built, so its `MODELS` type has fewer (and a few other) model
keys, and a few models of the published list are gone from the recording. A
`getModel` call whose model id exists only in one of the two catalogs
type-checks against the pin and not against the release, or the reverse.
`make test-sdk-ts` prints the difference as a report; it does not fail, because
the pin is compared with the build and not with the release.

`pi-pin.d.ts` imports Pi's other dependencies (`typebox`,
`@earendil-works/pi-mcp`, the provider SDKs) by name, and the compiler resolves
them from this package's directory. The package therefore declares them as peer
dependencies, at the versions the pinned Pi packages declare
(`make vendor-sdk-ts-pin-declarations` writes the list). npm 7 and later and
pnpm install peer dependencies by themselves. They are packages Pi's own install
already brings, at the same versions while the pin equals the release, so the
install then adds nothing.

With `npm install --install-links`, npm copies this folder into `node_modules`.
Without that flag npm links the folder, the compiler resolves those imports
from the folder itself, and the types that come from those packages are lost
without an error.
To keep a link, set `"preserveSymlinks": true` in `tsconfig.json`. Node's own
modules need `@types/node`, as Pi's published declarations do. Specifiers that
begin with `pi-pin:` are private to the bundle.

## Using the declarations

Import runtime values and shared types from Pi. Import the extended context and
PiG-only types from this package:

```ts
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { PiGLoginDefinition } from "@michaelkinsy/pig-extension-types";

// Importing the PiG declaration package augments Pi's ExtensionUIContext.

export default function extension(pi: ExtensionAPI): void {
  pi.on("session_start", async (_event, ctx) => {
    const login: PiGLoginDefinition = {
      brand,
      hero,
      mascot,
      palette,
      name: "Example Bot",
      description: "Custom coding agent",
      tagline: "Build with care.",
    };
    await ctx.ui.setLogin(login);
  });
}
```

Importing the package also augments pi-tui's `Component` with two optional
PiG-only properties for the component kit. `viewTheme` on a surface's root
component (the `ctx.ui.custom` component, a widget, header or footer, or a
renderer's result) overrides theme tokens for that surface, and
`viewLines` tells a frontend what the rows of a component with its own
`render` show:

```ts
import type { Component } from "@earendil-works/pi-tui";

const root: Component = container;
root.viewTheme = { accent: "#d75f00" };
table.viewLines = { list: { items: tracks.map((t) => ({ label: t.title, detail: t.artist })), selectedIndex } };
```

The terminal draws the override too. `viewLines` reaches only a frontend, and
its `list` needs one item per row.

Importing the package also declares `ExecOptions.maxBuffer` and
`ExecResult.truncated`, which `pi.exec()` has in Pi after the pinned 1.4.0
declarations (Pi commit `c466a359`): `maxBuffer` limits stdout and stderr
combined, and `truncated` is true when output exceeded it. The re-exported Pi
declarations stay the pinned ones.

PiG's Node host supplies Pi-compatible runtime shims. This package only provides
editor and compiler types for the PiG additions. The package stays private until
the public release process approves publication.
