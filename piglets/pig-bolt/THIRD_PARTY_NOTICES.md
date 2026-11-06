<!--
SPDX-License-Identifier: MIT
-->

# Third-Party Notices

This Piglet distributes no third-party binary. It downloads the compiler below to the user's machine and compiles executables there that contain the same code.

## Pi-Bolt ahead-of-time compiler 0.7.4

Source: <https://github.com/opensec-git/Pi-Bolt/tree/6088a1fe9395b10c666f6e25ba67c44b5d6453b8> (`sources.json` and `patches/` name the corresponding source of every component).

- Pi-Bolt: MIT.
- Bun: MIT.
- WebKit JavaScriptCore: LGPL-2.0-or-later and BSD-2-Clause. The corresponding source is available from the location above.
- ICU: Unicode-3.0.

The release archive's own `LICENSE`, `LICENSE.pi` and `THIRD_PARTY_NOTICES.md` are kept with the downloaded compiler and copied beside every executable it produces, in `licenses/`.
