<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Third-Party Notices

This file records third-party software that PiG distributes in its source tree or embeds in its output. The release SBOM records dependencies compiled or packaged for each release.

The canonical license texts for the components below are in `LICENSES/`.

## ICU 78.3 CJK word segmentation

Source: <https://github.com/unicode-org/icu/tree/release-78.3/icu4c/source>

Distributed files: `internal/wordsegmenter/cjk.go` and `internal/wordsegmenter/cjk_dictionary.bin`.

- Copyright © 2016 and later Unicode, Inc. and others.
- Copyright 2006–2016 International Business Machines Corporation and others.
- The dictionary includes the Google, TaBE, Academia Sinica, and IPADIC notices reproduced in `LICENSES/LicenseRef-ICU-CJK.txt`.
- License texts: `LICENSES/Unicode-3.0.txt` and `LICENSES/LicenseRef-ICU-CJK.txt`.

PiG translates ICU's weighted CJK word-boundary algorithm to Go. The embedded index is generated from the exact ICU 78.3 `cjdict.txt` data used by the pinned Node oracle. Its source SHA256 is `e73fd72048981d0cc13e9dc436a7eaba07ffb6eff58c8a59dc75c1df746663a0`.


## ICU 78.3 Southeast Asian word segmentation

Source: <https://github.com/unicode-org/icu/tree/release-78.3/icu4c/source>

Distributed files: `internal/wordsegmenter/sea.go`, `internal/wordsegmenter/segments.go`, `internal/wordsegmenter/word_rules.go`, `internal/wordsegmenter/rule_data.go`, the Thai/Lao/Khmer/Burmese `*_dictionary.bin` indexes, and the dictionary-derived differential corpus in `internal/wordsegmenter/testdata/sea-icu78.json`.

- Copyright © 2016 and later Unicode, Inc. and others.
- Copyright 1999–2016 International Business Machines Corporation and others.
- Copyright 2006–2015 International Business Machines Corporation, Apple Inc., and others.
- The Lao dictionary includes the notice of Brian Eugene Wilson and Robert Martin Campbell (2013).
- The Burmese dictionary includes the notice of LeRoy Benjamin Sharon (2013), with thanks to Robert Martin Campbell.
- Complete dictionary notices and redistribution terms: `LICENSES/LicenseRef-ICU-SEA.txt`.
- ICU algorithm and Unicode 17.0.0 property-data terms: `LICENSES/Unicode-3.0.txt`.

PiG translates ICU's `DictionaryBreakEngine`, `PossibleWord`, `ThaiBreakEngine`, `LaoBreakEngine`, `KhmerBreakEngine`, `BurmeseBreakEngine`, `DictionaryCache::populateDictionary` engine selection, `UnhandledEngine`, and word-rule tailoring to Go. `internal/wordsegmenter/generate_sea_dictionary.py` verifies each source hash and preserves ICU's byte-offset transform in a sorted embedded index. The generator and `internal/wordsegmenter/README.md` record the exact source hashes and regeneration procedure.

## Go gopher artwork

Source: <https://go.dev/brand>

The project banner contains an adaptation of the Go gopher, which was designed by Renee French.

- License: CC-BY-4.0
- License text: `LICENSES/CC-BY-4.0.txt`
- Distributed files: `docs/assets/pig-project-banner.png`

The remaining PiG artwork and composition are licensed under MIT.

## Contributor Covenant 2.1

Source: <https://www.contributor-covenant.org/version/2/1/code_of_conduct.html>

Distributed file: `.github/CODE_OF_CONDUCT.md`

- Copyright Contributor Covenant contributors
- License: CC-BY-4.0

PiG customizes the enforcement contact and preserves the required source and license attribution in the document.

## grok-mermaid 0.2.2

Source: <https://github.com/xl0/grok-mermaid/tree/v0.2.2>

Reviewed source revision: `3430584a71350d72b5a0310e91bcd55578a9f981`

Published npm integrity: `sha512-XcJEP5dDC8liHBh52mlLjU18fNvu1ckFsu0QpIG3+APZ270fsj9wxpiA6cOURmbUEuoMVgjbC2+UYgTdCqqgzA==`

Distributed files: `internal/mermaid/**`

- Copyright 2023-2026 SpaceXAI
- Copyright 2026 Alexey Zaytsev
- License: Apache-2.0

## unicode-width 0.2.0

Source: <https://github.com/unicode-rs/unicode-width/tree/v0.2.0>

Reviewed source revision: `79eab0d9fc2060783b43046f9611648dcd35172f`

Distributed file: `internal/mermaid/width_data.go`

- Copyright (c) 2015 The Rust Project Developers
- License: Apache-2.0 OR MIT; PiG uses the MIT option for this derived data

The width table is generated through `grok-mermaid` from the exact `unicode-width` 0.2.0 crate pinned by `grok-mermaid`'s width oracle. The required MIT copyright and permission notice is preserved here, and the canonical MIT text is in `LICENSES/MIT.txt`.

## Pi TUI 0.87.1

Source: <https://github.com/earendil-works/pi/tree/v0.87.1/packages/tui>

Distributed files: `coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/**`

- Copyright (c) 2025 Mario Zechner
- License: MIT
- License text: `LICENSES/MIT.txt`

`automation/gen/vendor-pi-dist.sh` copies Pi's complete JavaScript TUI module tree and native helper source/prebuilds from the locked coding-agent distribution. Only the `marked` and `get-east-asian-width` import paths change. The native prebuilds retain Pi's Linux, macOS, and Windows architecture directories. Pi's published TUI package contains no separate LICENSE file; this notice and the canonical MIT text retain its upstream license.

## OpenTUI-derived input buffering

Source: <https://github.com/anomalyco/opentui>

Distributed file: `internal/codingagent/stdin_buffer.go`

- Copyright (c) 2025 opentui
- License: MIT

Upstream Pi identifies its `stdin-buffer.ts` implementation as based on OpenTUI. PiG ports that implementation to Go.

## ansi-regex and strip-ansi

Sources:

- <https://github.com/chalk/ansi-regex>
- <https://github.com/chalk/strip-ansi>

Distributed files:

- `internal/codingagent/export/ansi_html.go`
- `internal/codingagent/tools/sanitize.go`

- Copyright (c) Sindre Sorhus (<https://sindresorhus.com>)
- License: MIT

Upstream Pi identifies portions of its ANSI utility as derived from these projects. PiG ports that behavior to Go.

## Highlight.js 11.9.0

Source: <https://github.com/highlightjs/highlight.js/tree/11.9.0>

Distributed file: `internal/codingagent/export/assets/vendor/highlight.min.js`

```text
BSD 3-Clause License

Copyright (c) 2006, Ivan Sagalaev.
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this
  list of conditions and the following disclaimer.

* Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.

* Neither the name of the copyright holder nor the names of its
  contributors may be used to endorse or promote products derived from
  this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## Marked 15.0.4

Source: <https://github.com/markedjs/marked/tree/v15.0.4>

Distributed file: `internal/codingagent/export/assets/vendor/marked.min.js`

```text
Marked

Copyright (c) 2018+, MarkedJS (https://github.com/markedjs/)
Copyright (c) 2011-2018, Christopher Jeffrey (https://github.com/chjj/)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

Markdown

Copyright © 2004, John Gruber
http://daringfireball.net/
All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
* Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
* Neither the name “Markdown” nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.

This software is provided by the copyright holders and contributors “as is” and any express or implied warranties, including, but not limited to, the implied warranties of merchantability and fitness for a particular purpose are disclaimed. In no event shall the copyright owner or contributors be liable for any direct, indirect, incidental, special, exemplary, or consequential damages (including, but not limited to, procurement of substitute goods or services; loss of use, data, or profits; or business interruption) however caused and on any theory of liability, whether in contract, strict liability, or tort (including negligence or otherwise) arising in any way out of the use of this software, even if advised of the possibility of such damage.
```

## TypeBox 1.3.27

The Node extension runtime serves TypeBox for the `typebox`, `typebox/value`, `typebox/compile`, and `@sinclair/typebox*` specifiers, as Pi does. `coding/extension/host/subprocess/runtime-node/shims/typebox*.mjs` are an esbuild bundle of the npm package `typebox@1.3.27` (integrity `sha512-zu+jc1pcy4UiNThxikUr36f0Rybk9PEeCg/NE6adeWr/SKsdNO4EzZHYRDlv2YCVAfj3Odq3dESSo/jNyoBXzA==`), produced by `automation/gen/vendor-typebox.sh`.

```text
The MIT License (MIT)

Copyright (c) 2017-2026 Haydn Paterson

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

## yaml 2.9.0

The Node extension runtime parses extension frontmatter with the same YAML library Pi 0.87.1 uses. `coding/extension/host/subprocess/runtime-node/shims/yaml/` is the unmodified ES module build (`browser/`) of the npm package `yaml@2.9.0` (integrity `sha512-2AvhNX3mb8zd6Zy7INTtSpl1F15HW6Wnqj0srWlkKLcpYl/gMIMJiyuGq2KeI2YFxUPjdlB+3Lc10seMLtL4cA==`), copied by `automation/gen/vendor-pi-dist.sh`.

```text
Copyright Eemeli Aro <eemeli@gmail.com>

Permission to use, copy, modify, and/or distribute this software for any purpose
with or without fee is hereby granted, provided that the above copyright notice
and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS
OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
THIS SOFTWARE.
```

## highlight.js 10.7.3

The Node extension runtime highlights code for Pi 0.87.1's `highlightCode` and `getMarkdownTheme` with the same highlighter Pi 0.87.1 uses. `coding/extension/host/subprocess/runtime-node/shims/highlight.js/` holds the unmodified CommonJS build (`lib/`), `package.json` and `LICENSE` of the npm package `highlight.js@10.7.3` (integrity `sha512-tzcUFauisWKNHaRkN4Wjl/ZA07gENAjFl3J/c480dprkGTg5EQstgaNFqBfUqCq54kZRIEcreTsAgF/m2quD7A==`), copied by `automation/gen/vendor-pi-dist.sh`.

```text
BSD 3-Clause License

Copyright (c) 2006, Ivan Sagalaev.
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this
  list of conditions and the following disclaimer.

* Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.

* Neither the name of the copyright holder nor the names of its
  contributors may be used to endorse or promote products derived from
  this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## marked 18.0.11

The Node extension runtime renders pi-tui's `Markdown` component with the same Markdown parser Pi 0.87.1's pi-tui uses. `coding/extension/host/subprocess/runtime-node/shims/marked/` holds the unmodified ES module build (`lib/marked.esm.js`), `package.json` and `LICENSE` of the npm package `marked@18.0.11` (integrity `sha512-HnslJfsZkRPBDJRHvVtAaWlZHEpSu7u8LgQuJCELjRKuWR+hpq4A7sLq3p8HaI9ypVoXDXxV34CsQJEe1+J5Aw==`), copied by `automation/gen/vendor-pi-dist.sh`.

```text
# License information

## Contribution License Agreement

If you contribute code to this project, you are implicitly allowing your code
to be distributed under the MIT license. You are also implicitly verifying that
all code is your original work. `</legalese>`

## Marked

Copyright (c) 2018+, MarkedJS (https://github.com/markedjs/)
Copyright (c) 2011-2018, Christopher Jeffrey (https://github.com/chjj/)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.

## Markdown

Copyright © 2004, John Gruber
http://daringfireball.net/
All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
* Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
* Neither the name “Markdown” nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.

This software is provided by the copyright holders and contributors “as is” and any express or implied warranties, including, but not limited to, the implied warranties of merchantability and fitness for a particular purpose are disclaimed. In no event shall the copyright owner or contributors be liable for any direct, indirect, incidental, special, exemplary, or consequential damages (including, but not limited to, procurement of substitute goods or services; loss of use, data, or profits; or business interruption) however caused and on any theory of liability, whether in contract, strict liability, or tort (including negligence or otherwise) arising in any way out of the use of this software, even if advised of the possibility of such damage.
```

## get-east-asian-width 1.6.0

The Node extension runtime measures character widths with the same library Pi 0.87.1's pi-tui uses. `coding/extension/host/subprocess/runtime-node/shims/get-east-asian-width/` is the unmodified npm package `get-east-asian-width@1.6.0` (integrity `sha512-QRbvDIbx6YklUe6RxeTeleMR0yv3cYH6PsPZHcnVn7xv7zO1BHN8r0XETu8n6Ye3Q+ahtSarc3WgtNWmehIBfA==`), copied by `automation/gen/vendor-pi-dist.sh`.

```text
MIT License

Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

## partial-json 0.1.7

The Node extension runtime parses streaming tool-call JSON with the same library Pi 0.87.1's pi-ai uses. `coding/extension/host/subprocess/runtime-node/shims/partial-json/` holds the unmodified `dist/` modules, `package.json` and `LICENSE` of the npm package `partial-json@0.1.7` (integrity `sha512-Njv/59hHaokb/hRUjce3Hdv12wd60MtM9Z5Olmn+nehe0QDAsRtRbJPvJ0Z91TusF0SuZRIvnM+S4l6EIP8leA==`), copied by `automation/gen/vendor-pi-dist.sh`.

```text
MIT License

Copyright (c) 2023 Promplate Dev Team

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

## ignore 7.0.8

Source: <https://github.com/kaelzhang/node-ignore>

Distributed files: `coding/extension/host/subprocess/runtime-node/shims/ignore/`.

- Copyright (c) 2013 Kael Zhang <i@kael.me>, contributors
- License: MIT
- Complete notice and license: `coding/extension/host/subprocess/runtime-node/shims/ignore/LICENSE-MIT`

## diff 8.0.4

Source: <https://github.com/kpdecker/jsdiff>

Distributed files: `coding/extension/host/subprocess/runtime-node/shims/diff/`.

- Copyright (c) 2009-2015, Kevin Decker <kpdecker@gmail.com>
- License: BSD-3-Clause
- Complete notice and license: `coding/extension/host/subprocess/runtime-node/shims/diff/LICENSE`

The runtime includes the pinned package's ES module implementation, package metadata and license. Type declarations are not shipped. Pi's own edit-diff module uses this implementation for display diffs and unified patches.

## cross-spawn 7.0.6 and dependencies

Source: <https://github.com/moxystudio/node-cross-spawn>

Distributed root: `coding/extension/host/subprocess/runtime-node/shims/cross-spawn/`. The package and its exact pinned production dependencies are copied without source changes by `automation/gen/vendor-node-dependencies.mjs`. Paths below are relative to this root. Each package retains its complete license text and copyright notice.

`internal/crossspawn` also translates cross-spawn's Windows command parsing, resolution and escaping, including shebang-command, shebang-regex, which and isexe, into Go. The Go translation retains the same MIT and ISC notices. Its drive-relative path resolution translates Node's `path.win32.resolve` (lib/path.js), Copyright Joyent, Inc. and other Node contributors, MIT, and retains that notice.

| Package | Version | Copyright | License | Notice path |
|---|---|---|---|---|
| cross-spawn | 7.0.6 | Copyright (c) 2018 Made With MOXY Lda <hello@moxy.studio> | MIT | `LICENSE` |
| path-key | 3.1.1 | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> | MIT | `node_modules/path-key/license` |
| shebang-command | 2.0.0 | Copyright (c) Kevin Mårtensson <kevinmartensson@gmail.com> | MIT | `node_modules/shebang-command/license` |
| shebang-regex | 3.0.0 | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> | MIT | `node_modules/shebang-command/node_modules/shebang-regex/license` |
| which | 2.0.2 | Copyright (c) Isaac Z. Schlueter and Contributors | ISC | `node_modules/which/LICENSE` |
| isexe | 2.0.0 | Copyright (c) Isaac Z. Schlueter and Contributors | ISC | `node_modules/which/node_modules/isexe/LICENSE` |

## jiti 2.7.0

The Node extension runtime loads TypeScript and JavaScript extensions with the same loader, release and options Pi 0.87.1 uses. `coding/extension/host/subprocess/runtime-node/shims/jiti/` holds the unmodified `lib/` and `dist/` directories, `package.json` and `LICENSE` of the npm package `jiti@2.7.0` (integrity `sha512-AC/7JofJvZGrrneWNaEnJeOLUx+JlGt7tNa0wZiRPT4MY1wmfKjt2+6O2p2uz2+skll8OZZmJMNqeke7kKbNgQ==`), copied by `automation/gen/vendor-pi-dist.sh`. As published, `dist/jiti.cjs` and `dist/babel.cjs` bundle jiti's own dependencies: Babel 7 and its plugins, acorn, mlly, pathe, get-tsconfig, json5 and others under the MIT license, and semver 6 under the ISC license.

```text
MIT License

Copyright (c) Pooya Parsa <pooya@pi0.io>

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

## Independent Node SDK dependencies

`automation/gen/vendor-pi-dist.sh` copies the following exact production dependencies from the locked Pi 0.87.1 installation. Paths are relative to `coding/extension/host/subprocess/runtime-node/shims/`. Each package retains its published manifest and complete license. The Photon package includes its published WebAssembly image-processing asset; it is not an extension runtime.

| Package | Version | License | Retained notice and license |
|---|---|---|---|
| chalk | 6.0.0 | MIT | `chalk/license` |
| undici | 8.10.2 | MIT | `undici/LICENSE` |
| semver | 7.8.5 | ISC | `semver/LICENSE` |
| minimatch | 10.2.6 | BlueOak-1.0.0 | `minimatch/LICENSE.md` |
| brace-expansion | 5.0.9 | MIT | `minimatch/node_modules/brace-expansion/LICENSE` |
| balanced-match | 4.0.4 | MIT | `minimatch/node_modules/brace-expansion/node_modules/balanced-match/LICENSE.md` |
| hosted-git-info | 9.0.3 | ISC | `hosted-git-info/LICENSE` |
| lru-cache | 11.4.0 | BlueOak-1.0.0 | `hosted-git-info/node_modules/lru-cache/LICENSE.md` |
| grok-mermaid | 0.2.3 | Apache-2.0 | `grok-mermaid/LICENSE` |
| @silvia-odwyer/photon-node | 0.3.4 | Apache-2.0 | `photon-node/LICENSE.md` |
| proper-lockfile | 4.1.2 | MIT | `proper-lockfile/LICENSE` |
| retry | 0.12.0 | MIT | `proper-lockfile/node_modules/retry/License` |
| graceful-fs | 4.2.11 | ISC | `proper-lockfile/node_modules/graceful-fs/LICENSE` |
| signal-exit | 3.0.7 | ISC | `proper-lockfile/node_modules/signal-exit/LICENSE.txt` |

The Blue Oak Model License is also reproduced in `LICENSES/BlueOak-1.0.0.txt`. Copyright holders appear in the retained files and `NOTICE`. No dependency resolves from the network at extension load time.

`internal/nodesemver` translates the semver 7.8.5 version and range parsing that Pi's package manager calls into Go. The Go translation retains the same ISC notice, Copyright (c) Isaac Z. Schlueter and Contributors, reproduced in `LICENSES/ISC.txt`.

## libuv 1.51.0 Windows command-line quoting

Source: <https://github.com/libuv/libuv/blob/v1.51.0/src/win/process.c>

`internal/nodespawn` translates libuv's `quote_cmd_arg` and `make_program_args`, which build the Windows command line for Node's `child_process.spawn`, into Go. The Go translation retains the libuv notice, Copyright Joyent, Inc. and other Node contributors, under the MIT license reproduced in `LICENSES/MIT.txt`.

## Go encoding/json

`extensions/sdk/json/` derives from the Go Authors' Go 1.27.1 `src/encoding/json` implementation, with lossless UTF-16 surrogate handling for the extension wire. It retains the Go Authors' copyright headers and BSD-3-Clause license in `extensions/sdk/json/LICENSE`. `extensions/sdk/json/README.md` records the source files and local modifications. The SDK source bundle carries this license.

## Unicode 17.0.0 character data

The generated `tui/widthx/unicode_tables.go` derives character properties and emoji sequences from the Unicode Character Database and Unicode emoji data, pinned in `tui/widthx/gen/inputs.sha256`. Copyright © 1991-2026 Unicode, Inc. Used under Unicode License V3, reproduced in `LICENSES/Unicode-3.0.txt` (source: https://www.unicode.org/license.txt). This data license supplements the MIT license for the Go implementation.
