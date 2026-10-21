import { SAMPLES, paletteOf } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

// A theme Pi has by name, in each color mode; the system theme and a name no theme has (initTheme falls back to the system theme) report only the name, since
// the system theme is generated from the terminal's colors.
export const cases = [
  { name: "dark truecolor", args: ["dark", "truecolor"] },
  { name: "light truecolor", args: ["light", "truecolor"] },
  { name: "dark 256color", args: ["dark", "256color"] },
  { name: "light 256color", args: ["light", "256color"] },
  { name: "system", args: ["system", "truecolor"] },
  { name: "unknown name", args: ["no-such-theme", "truecolor"] },
  { name: "undefined name", args: [undefined, "truecolor"] },
  { name: "empty name", args: ["", "truecolor"] },
];

export async function call(fn, name, colorMode) {
  const tui = await this.load("pi-tui");
  if (colorMode === "truecolor") process.env.COLORTERM = "truecolor";
  else delete process.env.COLORTERM;
  tui.resetCapabilitiesCache();
  fn(name);
  const theme = globalThis[Symbol.for("@earendil-works/pi-coding-agent:theme")];
  if (theme.name === "system") return { name: theme.name };
  return { name: theme.name, palette: paletteOf(theme), fg: SAMPLES.map((text) => theme.fg("accent", text)), bold: SAMPLES.map((text) => theme.bold(text)) };
}
