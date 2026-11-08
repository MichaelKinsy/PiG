# lovely-mermaid

Render Mermaid diagrams as Unicode box-drawing art, for terminals.

No browser, no headless Chrome, no SVG — a self-contained layout engine that
emits text. Streaming-friendly, colour-ready through semantic spans, zero
dependencies.

```
      ┌──────────────┐
      │ Parse source │
      └───────┬──────┘
              │
              ▼
       ╔════════════╗
       ║ Supported? ║
       ╚══════╤═════╝
      ┌───────┴────────┐
      ▼yes             ▼no
 ┌─────────┐   ┌───────────────┐
 │ Lay out │   │ Framed source │
 └────┬────┘   └───────┬───────┘
      └───────┬────────┘
              ▼
       ┌─────────────┐
       │ Unicode art │
       └─────────────┘
```

Try it live: **[xl0.github.io/lovely-mermaid](https://xl0.github.io/lovely-mermaid/)** —
paste a diagram, pick a theme, watch it stream.

## Install

```sh
npm install lovely-mermaid
```

## Usage

```ts
import { render } from 'lovely-mermaid'

const art = render('flowchart LR\n  A[Start] --> B[Done]')
if (art) console.log(art.plain.join('\n'))
```

`render` draws the diagram at whatever size it needs and reports that as
`art.width`. It returns `null` when there is no art to show: blank input, a
diagram type it does not draw, or a source in which not one statement parsed.

### Syntax errors

Rendering is best-effort in every grammar: a source that does not fully parse
still draws what it can, and reports the rest in `art.warnings`.

```ts
// Flowcharts keep the parseable prefix of a statement, as mermaid.js does.
render('graph TD\n A[Start --> B')
// plain     one box labelled `Start --> B` — the edge you wrote is gone
// warnings  ['node "A": label is missing its closing `]`']

// The other grammars drop unreadable statements individually.
render('stateDiagram-v2\n A --> B\n some garbage line')
// plain     the A --> B transition, drawn
// warnings  ['dropped, unreadable statement: "some garbage line"']
```

A diagram that outgrows a size cap (128 nodes, 512 edges) renders its prefix
with a `diagram truncated: …` warning instead of disappearing — a streamed
diagram can never shrink back under a cap, so a stable truncated render beats
flipping to the source box for good.

An empty `warnings` means the whole source made it into the art.

**Warnings are advisory — never gate rendering on them.** The art is the best
available drawing either way, and a diagram mid-edit warns at nearly every
intermediate state: a label bracket is unterminated right up until it is typed.

`diagramKind(src)` reads the header alone, separating the two `null`s worth
different messages:

```ts
if (render(src) === null) {
  const kind = diagramKind(src) // 'flowchart' | 'state' | … | 'gitgraph' | null
  console.log(kind ? `${kind} diagram: syntax error` : 'diagram type not supported here')
}
```

### Streaming

Call `render` on each prefix as it arrives — no special handling, no waiting for
a complete diagram. Best-effort parsing is what keeps it drawn instead of
alternating with the source box.

<img src="https://raw.githubusercontent.com/xl0/lovely-mermaid/master/packages/lovely-mermaid/docs/streaming.gif" width="900" alt="A terminal replaying a stream of Mermaid diagrams — flowcharts and a state machine drawing themselves as box-drawing art, each one growing in place without ever reverting to a block of source text.">

### Fitting a viewport

The renderer takes no width limit. Nothing about a terminal tells it whether a
wide diagram should be shrunk, scrolled, linked to an image or just printed, so
the decision stays with you — compare `art.width` against the space you have:

```ts
import { render, sourceBox } from 'lovely-mermaid'

const cols = process.stdout.columns
const art = render(src)
if (art && art.width <= cols) console.log(art.plain.join('\n'))
else {
  console.log(sourceBox(src, cols).plain.join('\n'))
  console.log(`(diagram needs ${art?.width ?? '?'} columns)`)
}
```

`sourceBox(src, maxWidth?)` frames the source in a titled box, hard-wrapping to
`maxWidth`. It is the usual thing to show when the art does not fit or does not
exist, but it is yours to choose and yours to caption.

### Colour

The core is colour-blind. `styled` carries the same rows as `plain`, split into
runs tagged with a role — what a cell *is* — so you map roles to your own
theme:

<img src="https://raw.githubusercontent.com/xl0/lovely-mermaid/master/packages/lovely-mermaid/docs/demo.svg" width="330" alt="A flowchart rendered as Unicode box-drawing art on a dark panel: grey box outlines, white node labels, cyan connectors and arrowheads, grey edge labels.">

That image is real `render()` output painted through one such theme.

```ts
import { type Role, render } from 'lovely-mermaid'

const art = render(src)!

const theme: Partial<Record<Role, (s: string) => string>> = {
  border: dim, text: white, edge: cyan, edgeLabel: gray,
}
const out = art.styled.map((row) =>
  row.map((span) => (theme[span.role] ?? identity)(span.text)).join(''),
)
```

| Role | What it covers |
| --- | --- |
| `border` | box outlines, subgraph frames, compartment rules |
| `text` | node, participant and compartment labels |
| `edge` | connector lines and arrowheads |
| `edgeLabel` | text sitting on an edge |
| `title` | the `mermaid: <kind>` header of a source box |
| `none` | blank filler |

`styled[i]` joined is always exactly `plain[i]`, so you can swap between them
freely. A render is plain JSON: cacheable across theme changes, transferable to
a worker.

For the common case there is a helper:

```ts
import { render, toAnsi } from 'lovely-mermaid'

console.log(toAnsi(render(src)!).join('\n'))
```

`toAnsi(art, theme)` takes `Partial<Record<Role, string>>` of SGR parameters
(`'2'` dim, `'36'` cyan, `'38;5;244'` for 256-colour), defaulting to a dim
frame with cyan connectors.

### Author classes

Roles are what the renderer decided a cell is; classes are what the author
assigned. `A:::hot` and `class A,B hot` tag nodes, and the cells those nodes
paint carry the names out through `span.classes`. `classDef` declarations are
parsed and surfaced on `art.classDefs`, uninterpreted:

```ts
const art = render('flowchart TD\n A[DB]:::hot --> B\n classDef hot fill:#f96')!
art.classDefs               // { hot: { fill: '#f96' } }
art.styled.flat().find((s) => s.text.includes('DB'))?.classes  // ['hot']
```

`toAnsi` applies them best-effort on top of the role theme: `fill` backs the
node's cells, `stroke` colours its border, `color` its text,
`font-weight:bold` bolds — as truecolor SGR, with a readable black/white
foreground picked when a fill declares no colour. Consumers with their own
styling model use the exported `resolveClassStyle`/`contrastOn` against the
classed spans instead.

### Links

`click A "https://…"` (flowchart) and `link A "https://…"` (class diagrams)
land on the node's spans as `span.href`. `toAnsi` emits them as OSC 8
hyperlinks — clickable in every modern terminal, invisible in the rest:

```ts
render('flowchart TD\n A[Docs] --> B\n click A "https://example.com/docs"')
// toAnsi(art) wraps A's cells in ESC ]8;; … — the whole box is the link
```

## Supported diagrams

| Type | Notes |
| --- | --- |
| `graph` / `flowchart` | `TD`/`TB`, `BT`, `LR`, `RL`; `subgraph` nesting; node shapes incl. v2 `A@{shape: cyl, label: …}`; solid/dotted/thick links; arrow, circle, cross heads; edge labels |
| `stateDiagram` / `stateDiagram-v2` | states, transitions, `[*]` start/end, `<<choice>>`, descriptions, composite states as nested frames with `--` regions |
| `classDiagram` | compartments, annotations, generics, per-end cardinalities, inheritance/realization/composition/aggregation/dependency |
| `erDiagram` | entities, attributes, crow's-foot cardinalities at their own ends |
| `sequenceDiagram` | participants, messages, self-messages, activations, notes, `loop`/`alt`/`opt` dividers, `autonumber` |
| `pie` | a labelled bar list — proportions read better than circles in a terminal; `showData` appends the raw values |
| `mindmap` | the indentation tree, drawn with `├──`/`└──` guides |
| `timeline` | periods and events as a vertical list, sections as headers |
| `gitGraph` | commit lanes in `git log --graph` style: newest on top, branch heads, tags, merges — even across lanes |

YAML frontmatter is understood: its `title:` is drawn above the diagram, the
rest is skipped. `:::class` tags and `classDef`s work in every grammar that
has them.

## Origin and credits

lovely-mermaid began as a byte-faithful TypeScript port of the terminal
Mermaid renderer inside the Grok CLI
([xai-org/grok-build](https://github.com/xai-org/grok-build),
`crates/codegen/xai-grok-markdown/src/mermaid.rs`), briefly published as
`grok-mermaid`. It has since gone its own way — lenient parsing built for
streaming, semantic colour spans, author classes, grapheme-correct width —
but the Sugiyama layout bones trace back to that original.

Also shout out to Simon Willison's
[grok-mermaid.html](https://tools.simonwillison.net/grok-mermaid)
([source](https://github.com/simonw/tools/blob/main/grok-mermaid.html)), which
compiles the Rust renderer to WebAssembly so it runs in a browser.

## License

Apache-2.0. See [LICENSE](LICENSE).

The Rust original in [xai-org/grok-build](https://github.com/xai-org/grok-build)
(`crates/codegen/xai-grok-markdown/src/mermaid.rs`) is Apache-2.0, Copyright
2023-2026 SpaceXAI. Its layout algorithms, glyph tables, parser behaviour and
test corpus are what this port is derived from.
