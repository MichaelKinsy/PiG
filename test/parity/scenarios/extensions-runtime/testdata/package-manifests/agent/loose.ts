import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
const description: string = "loose TypeScript loaded";
export default function (pi: ExtensionAPI): void {
  pi.registerCommand("loose-probe", { description, handler: async () => {} });
}
