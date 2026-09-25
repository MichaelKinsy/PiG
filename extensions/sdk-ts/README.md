# PiG TypeScript extension declarations

This package adds PiG-specific declarations to the exact TypeScript extension
API from Pi 0.87.1. It contains no runtime implementation.

Install the declarations from this source tree with the pinned Pi package:

```bash
npm install --save-dev \
  /path/to/PiG/extensions/sdk-ts \
  @earendil-works/pi-coding-agent@0.87.1
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

PiG's Node host supplies Pi-compatible runtime shims. This package only provides
editor and compiler types for the PiG additions. The package stays private until
the public release process approves publication.
