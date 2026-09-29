import widgetFactory from "./widget-factory.mjs";
import { complete } from "@earendil-works/pi-ai";
export default function (pi) {
  if (typeof complete !== "function") throw new Error("pi-ai complete did not load");
  widgetFactory(pi);
}
