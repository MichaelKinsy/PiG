# Changelog

## [Unreleased]

## [0.3.3] - 2026-08-15

### Changed

- Adjacent-rank back edges (TD/BT) return locally beside their forward edge instead of routing around the diagram, as mermaid draws them.
- Skip edges (TD/BT) leave through the source's bottom fan and enter through the target's top, dropping straight down when the column is clear; side lanes remain the fallback and pack shortest-span-innermost to minimise crossings.
- A node's top entries spread evenly across the box top; an entry label that does not fit before the next entry renders left of its arrowhead.
- LR/RL skips whose target row is clear run straight through off the source's right-side fan instead of detouring via the bottom lane.
- A labelled lane keeps its own row, with the label set into its line (`── flaky ──`), instead of merging onto a shared row where the label appeared to cover other edges.
- LR/RL column gaps size to the labels passing through each gap, not the diagram's widest label, tightening label-heavy layouts.
- Labels of parallel edges (same source and target) join as `one / two`; previously every label after the first silently vanished.
- Repeated state descriptions (`s1 : a` then `s1 : b`) accumulate into one wrapped label instead of last-one-wins.
- A bare timeline period line renders event-less instead of being dropped.

### Fixed

- Kebab-case flowchart ids parse as one id: `step-1-->step-2` no longer mangles into a self-loop plus a stray node.
- `class Animal["Label"]` declares a class titled by its label instead of refusing the diagram or forking into two nodes.
- A state transition label containing `-->` (e.g. `A --> B: go "x --> y"`) stays one label instead of inventing a phantom state.
- A `-x`/`-)` sequence operator embedded in a hyphenated participant name (`pre-x->>B`) no longer invents a participant; hyphenated names work throughout.
- Quoted ER entity names render without their quotes; an unterminated ER alias bracket now warns.
- Quoted gitGraph branch names (`branch "feat x"`) work in `branch`/`checkout`/`merge`.
- `BT` renders multi-row content in reading order (wrapped labels, class compartments, frame titles); arrows and box-drawing characters inside labels survive `RL`/`BT` flips.
- A literal tab in a label paints as a space, keeping terminal columns aligned and `width` accurate.
- Flowchart `|labels|` are quote-aware: `A -->|"a|b"| B` keeps its pipe.
- A flowchart that hits the node cap reports one truncation warning, without a spurious per-statement drop warning beside it.

## [0.3.2] - 2026-08-15

### Added

- `click A "url"` (flowchart) and `link A "url"` (class diagrams) surface on the node's spans as `span.href`; `toAnsi` emits them as OSC 8 terminal hyperlinks.

### Changed

- Pie bars draw their unfilled remainder as a `░` track, so every bar shows its full scale.

### Fixed

- Class-diagram class assignments parse instead of being dropped: `class Agent focus` (the flowchart-style form) and `cssClass "A,B" name`.

## [0.3.1] - 2026-08-15

### Added

- Four new diagram types: `pie` (a labelled bar list with eighth-block precision; `showData` appends raw values), `mindmap` (the indentation tree with `├──`/`└──` guides), `timeline` (periods and events as a vertical list, sections as headers) and `gitGraph` (`git log --graph`-style commit lanes: newest on top, branch heads, tags, and merges that cross lanes cleanly). `diagramKind` reports the new kinds.

### Changed

- Sequence activations draw the active lifeline span as a double line (`║`, junctions `╟` `╢`) — two rails echoing mermaid's slim activation rectangle — instead of a thick line.

## [0.3.0] - 2026-08-15

### Breaking Changes

- Renamed the package to `lovely-mermaid` (formerly `grok-mermaid`); the repository moved to `xl0/lovely-mermaid` and the library now lives in `packages/lovely-mermaid` of a workspace.
- Renamed the span classification from `Cls`/`span.cls` to `Role`/`span.role`, freeing the word "class" for mermaid's author-assigned classes. `AnsiTheme` keys by `Role`.
- `MermaidArt` gained a required `classDefs` field.

### Added

- Composite states draw as titled frames: `state X { ... }` nests, `--` splits a composite into side-by-side regions, and `[*]` is scoped per composite.
- The flowchart v2 node syntax `id@{shape: cyl, label: "..."}` parses; shape names map onto the terminal's silhouettes.
- Sequence activations (`->>+` / `-->>-`, `activate`/`deactivate`) thicken the lifeline over the active range.
- ER and class-diagram cardinalities render at their own edge ends with the verb between (`0..*` shortens to `*`), and ER entity aliases may be quoted (`a["Bank Account"]`).
- Author classes are surfaced: `A:::name` and `class A,B name` land on the spans of the cells that node paints (`span.classes`), and `classDef` declarations are parsed into `art.classDefs` (`{ name: { fill: '#f96', … } }`). The renderer never interprets either.
- `toAnsi` applies classDef styles best-effort on top of the role theme: `fill` backs the node's cells, `stroke` colors its border, `color` its text, `font-weight:bold` bolds (merging with the role theme rather than replacing it) — as truecolor SGR, with a black/white foreground picked by luminance when a fill declares no color. The interpreter is exported (`resolveClassStyle`, `classSgr`, `contrastOn`) for consumers with their own styling model; every other property is ignored.
- A frontmatter `title:` is rendered, centred above the art in the `title` role.

### Fixed

- Flowchart inline edge labels containing `=`, `.` or `-` — quoted (`A --"a=b"--> B`) or not (`A -- a=b --> B`) — render their label instead of being dropped or mangled; the label now runs to the closing operator ([#2](https://github.com/xl0/lovely-mermaid/issues/2)).
- A quoted class-diagram cardinality containing `..` (`Customer "0..*" --> Order`) no longer reads as a dotted-link operator; the relation renders with the cardinality at its end.
- YAML frontmatter (`---` … `---`), part of the mermaid grammar since v10, is skipped instead of being mistaken for an unknown diagram header.
- A `:::` tag inside a quoted label or a description is text again, not a class assignment; tags are read inline at each id token, including before a description colon (`S1:::hot : waiting`).
- `class A, B warn` assigns both ids the class; a space after the comma no longer folds the rest of the id list into the class name.
- `classDef c fill:rgb(255, 0, 0)` keeps the function value whole instead of shredding it at the commas, making `rgb()` colors reachable.
- A sequence activation arriving exactly at the item cap no longer makes `render()` throw; the diagram truncates with a warning like any other capped input.
- A state `--` region containing an earlier-declared composite no longer silently deletes that subtree.
- `diagramKind` strips control characters the way `render` does, so the two agree on any input.
- A note left of the first participant is drawn beside the diagram instead of on top of its lifelines.
- The source box expands tabs to spaces (a literal tab misaligned the frame at the terminal's tab stops) and no longer overflows the argument limit on very large sources.

### Changed

- Diamond nodes — flowchart `A{...}` and state `<<choice>>` — draw as double-bordered boxes (`╔═╗`) instead of being indistinguishable from rounded ones; edges tee into the double line through the mixed glyphs (`╤` `╧` `╟` `╢`).
- Skip and back edges no longer cut through boxes ordered beyond their endpoints: nodes touching a lane edge order last within their rank, keeping the corridor to the lane clear.
- Every grammar is now lenient. State, class, ER and sequence diagrams drop an unreadable statement with a warning and render the rest, instead of refusing the whole diagram; the one-final-line salvage retry is gone, subsumed by the general rule. A source in which nothing parses still returns `null`.
- Size caps truncate instead of refusing: a diagram over 128 nodes / 512 edges renders its prefix with a `diagram truncated: …` warning. A streamed diagram that outgrows a cap keeps its stable render instead of flipping to the source box forever.
- Diagram headers are matched exactly: `stateDiagramFoo` no longer parses as a state diagram, matching mermaid proper. `classDiagram-v2` is now recognised.
- CI now stages releases on npm instead of publishing directly. Builds keep OIDC provenance; the release script waits for the staged version, then asks for a 2FA code and approves it — publishing stays a manual act.

## [0.2.3] - 2026-08-11

### Fixed

- Parse and discard `:::class` for flowcharts, state and class diagrams.

## [0.2.2] - 2026-08-04

### Removed

- Removed the `NOTICE` file. Upstream ships none, so Apache-2.0 §4(d) never applied; keeping one only pushed a propagation obligation onto redistributors. Attribution now lives in `README.md` and `LICENSE`.

## [0.2.1] - 2026-08-01

### Added

- Added npm trusted publishing with build provenance. Releases are now built, signed and published by CI from a matching `v*` tag.

### Fixed

- Fixed leading and trailing empty rows around rendered diagrams.

## [0.2.0] - 2026-07-28

### Breaking Changes

- Changed `render()` to return natural-width art with a reported `width`; removed `maxWidth` and automatic source fallback. Callers can use `sourceBox()` when needed.

### Added

- Added `diagramKind()` and advisory parse warnings so callers can distinguish unsupported diagrams from malformed or partially rendered input.
- Added best-effort recovery for strict grammars by dropping and reporting one unreadable final line.

## [0.1.1] - 2026-07-28

## [0.1.0] - 2026-07-28

Initial public release.

### Added

- Added Unicode terminal rendering for flowchart, state, class, ER, and sequence diagrams with semantic styled spans and ANSI themes.
