import { readFileSync } from "node:fs";
import * as fs from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
import { Container } from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/tui.js";
import { Image } from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/components/image.js";
import { Text } from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/components/text.js";
import { Spacer } from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/components/spacer.js";
import { setCapabilities } from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/terminal-image.js";

const root = new URL("../../../.upstream/current/packages/coding-agent/src/modes/interactive/", import.meta.url);
const load = name => readFileSync(new URL(`components/${name}.ts`, root), "utf8").replace(/^import .*;\n/gm, "").replaceAll("export class", "class");
const colors = JSON.parse(process.argv[2]);
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
const context = vm.createContext({ Container, Image, Text, Spacer, fs,
  getBundledInteractiveAssetPath: name => fileURLToPath(new URL(`assets/${name}`, root)),
  theme: { fg: (token, text) => colors[token] + text + "\x1b[39m", bold: text => "\x1b[1m" + text + "\x1b[22m" },
});
vm.runInContext(stripTypeScriptTypes(load("dynamic-border") + "\n" + load("earendil-announcement")) + "\nglobalThis.component = new EarendilAnnouncementComponent();", context);
console.log(JSON.stringify([1, 12, 32, 56, 80, 120].map(width => context.component.render(width))));
