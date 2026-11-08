// The page's packages: React and its DOM renderer, as the client tree vendors them (a Go app has no `npm install` of its own; an
// app that does imports `react` and `react-dom/client` by name).

import * as React from "../../../chord/nextnode/surfaces/client/pi/node_modules/react/index.js";
import * as jsx from "../../../chord/nextnode/surfaces/client/pi/node_modules/react/jsx-runtime.js";
import { createRoot } from "../../../chord/nextnode/surfaces/client/pi/node_modules/react-dom/client.js";

export { createElement, useEffect, useState } from "../../../chord/nextnode/surfaces/client/pi/node_modules/react/index.js";
export { createRoot, jsx, React };
