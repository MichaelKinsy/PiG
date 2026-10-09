# PiG TypeScript extension declarations

This package adds PiG-specific declarations to the exact TypeScript extension
API from Pi 1.1.0. It contains no runtime implementation.

Install the declarations from this source tree with the pinned Pi package:

```bash
npm install --save-dev \
  /path/to/PiG/extensions/sdk-ts \
  @earendil-works/pi-coding-agent@1.1.0
```

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

PiG's Node host supplies Pi-compatible runtime shims. This package only provides
editor and compiler types for the PiG additions. The package stays private until
the public release process approves publication.
