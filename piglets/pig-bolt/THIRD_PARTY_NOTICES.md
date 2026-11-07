<!--
SPDX-License-Identifier: MIT
-->

# Third-Party Notices

This Piglet distributes no third-party binary. It downloads the compiler below to the user's machine and compiles executables there that contain the same code.

## Pi-Bolt ahead-of-time compiler 1.1.2

Source: <https://github.com/opensec-git/Pi-Bolt/tree/1098b1ce19cf67daf8ab0995d7ef7e2a2bea8681> (`sources.json` and `patches/` name the corresponding source of every component).

- Pi-Bolt: MIT.
- Bun: MIT.
- WebKit JavaScriptCore: LGPL-2.0-or-later and BSD-2-Clause. The corresponding source is available from the location above.
- ICU: Unicode-3.0.

The release archive's own `LICENSE`, `LICENSE.pi` and `THIRD_PARTY_NOTICES.md` are kept with the downloaded compiler and copied beside every executable it produces, in `licenses/`.

## Pi-Bolt `rewriteCopy`, ported to Go

`extensions/boltruntime/rewrite.go` ports the scanner `rewriteCopy` and the code it substitutes (`requireFrom`, `resolveFrom`, `metaResolveFrom`, the `import.meta` table) from
`packages/coding-agent/src/gen-plugin-manifest.mjs` of Pi-Bolt (<https://github.com/opensec-git/Pi-Bolt/blob/1098b1ce19cf67daf8ab0995d7ef7e2a2bea8681/packages/coding-agent/src/gen-plugin-manifest.mjs>), so that an
extension compiled in by this Piglet reads its own location as one compiled in by `pi-bolt compile` does. The Go port decides what a module declares from tokens
instead of text and adds the TypeScript cast `(import.meta as T).member`; it leaves out Pi-Bolt's renaming of Pi's module names, which the build's tsconfig covers.

```
MIT License

Copyright (c) 2025 Mario Zechner
Copyright (c) 2026 OpenSec (Pi-Bolt)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
