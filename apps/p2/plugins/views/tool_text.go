package views

// toolDescription is the `view` tool's description and pageAPI what its help action answers, as upstream's tool.ts has them (vendored under
// web/upstream: bridge_test.go compares them).
const toolDescription = `Views: panes beside the transcript that the person sees and works in. Open one when seeing or editing something beside the chat helps more than text (a document, the changes, an editor you build for what you work on together); update views rather than opening new ones. Every viewer of the session sees them.
Kinds: markdown (path: a file relative to your working directory, rendered again when it changes), changes (the working tree's diffs; optional path narrows them), custom (html: a page you write, with JSON state; call help before writing one).
Actions: list; open (kind, title; id updates and shows an existing view; path; html, or artifact to copy an HTML artifact; state); read (id: its state, the person's selection, its page's errors, annotations; id transcript: the transcript's annotations); update (id: title, path, html, state to replace, or merge to add keys); close (id); arrange (layout): lay out the person's panes, e.g. {row: ['transcript', {column: ['plan', {tabs: ['glyph', 'notes']}]}], sizes: [1, 2]} as JSON: a node is a view ID ('transcript' is the chat), {tabs: [IDs], active: ID} or {row or column: [nodes], sizes: [shares]}; views left out are hidden, and the person may rearrange afterwards; resolve (annotations: ids, or id for all of a view's); save (id, name, description), saved, load (name): the owner's library, owner turns only; help: the custom page API.
What the person selects and annotates (comments, text to remove, drawings) arrives with their messages in a <views> block.`

const pageAPI = `A custom view is a whole HTML page (at most 2 MiB) in a sandboxed frame with no app identity; it may load https CDN resources. It talks to p2 through window.p2view:
- p2view.onState(fn): calls fn(state, from) with the view's state now (from: init) and at each change made elsewhere (from: update, by you or another device). Render from it.
- p2view.setState(next): saves the state (any JSON, at most 256 KiB; or a function of the current state). Call it on every edit; saving waits for a short pause.
- p2view.select(summary, data) / p2view.clearSelection(): the person's selection. summary is one line you read with their next message; data is what read shows.
- p2view.readOnly: true for viewers who cannot change the session; their changes are not saved.
- p2view.onDescribeRegion(fn): fn({x, y, width, height}) returns text naming what lies there (CSS pixels of the page), for drawings the person makes over it; without it, the labels and texts of the elements there (aria-label or data-label attributes help).
Keep the document's own data in the state, not only in the page: the state is what you read and change, and what a saved view keeps. Script errors the page logs show when you read the view.`
