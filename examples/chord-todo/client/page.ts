// The todo app's web entry, the base bundle the server builds at start: `startPage` with the app's web modules and React, a
// core facet serving the `Panels` registry, and a page showing every panel the plugins contribute.

import { defineFacet } from "../../../chord/nextnode/surfaces/client/pi/chord/src/index.ts";
import { HostProvider, useRegistry } from "../../../chord/nextnode/surfaces/client/pi/chord/src/react/index.ts";
import { startPage } from "../../../chord/nextnode/surfaces/client/pi/chord/src/web/index.ts";
import { createElement as h, createRoot, jsx, React } from "./react.ts";
import * as v1 from "./v1.ts";
import * as v1web from "./web.ts";
import "./style.css";

/** The page's own facet: it serves `Panels`, composing the contributions in plugin-name order. */
const core = defineFacet((host) => host.serve(v1web.Panels, { compose: (table) => table.map(({ value }) => value) }));

function Page() {
	const panels = useRegistry(v1web.Panels) ?? [];
	return h(
		"main",
		null,
		h("h1", null, "Todo"),
		...panels.map(({ name, Component }) => h("section", { key: name }, h(Component))),
	);
}

void startPage({
	connect: () => new WebSocket(`${location.origin.replace(/^http/, "ws")}/chord`),
	modules: { "@todo/app/v1": v1, "@todo/app/v1/web": v1web, react: React, "react/jsx-runtime": jsx },
	core: [{ name: "@todo/web", facet: core }],
	render: (host) => createRoot(document.getElementById("root")!).render(h(HostProvider, { host }, h(Page))),
});
