import { existsSync, readFileSync, realpathSync } from "fs";
import { dirname, join } from "path";
import { pathToFileURL } from "url";

const samplePngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aK9sAAAAASUVORK5CYII=";

const PI_PACKAGE_NAMES = ["@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent"];

const findPiPackageRoot = (): string => {
  let dir = dirname(realpathSync(process.argv[1] ?? process.execPath));
  while (true) {
    const packageJSON = join(dir, "package.json");
    if (existsSync(packageJSON)) {
      try {
        const pkg = JSON.parse(readFileSync(packageJSON, "utf8"));
        if (PI_PACKAGE_NAMES.includes(pkg?.name)) {
          return dir;
        }
      } catch {
        // keep walking
      }
    }
    const parent = dirname(dir);
    if (parent === dir) {
      throw new Error("could not locate pi-coding-agent package root");
    }
    dir = parent;
  }
};

const piCodingAgentRoot = findPiPackageRoot();

// Try earendil-works first, fall back to mariozechner.
const piTuiRoot = (() => {
  const earendil = join(piCodingAgentRoot, "node_modules/@earendil-works/pi-tui");
  if (existsSync(earendil)) return earendil;
  return join(piCodingAgentRoot, "node_modules/@mariozechner/pi-tui");
})();

const loadClipboardImageModule = async () =>
  import(pathToFileURL(join(piCodingAgentRoot, "dist/utils/clipboard-image.js")).href);

const withAgentFixturePath = async (fn: () => Promise<void>) => {
  const agentDir = process.env.PI_CODING_AGENT_DIR;
  const fixtureBin = agentDir ? join(agentDir, "bin") : "";
  if (!fixtureBin || !existsSync(fixtureBin)) {
    await fn();
    return;
  }
  const oldPath = process.env.PATH ?? "";
  process.env.PATH = `${fixtureBin}:${oldPath}`;
  try {
    await fn();
  } finally {
    process.env.PATH = oldPath;
  }
};

const loadTuiModules = async () => {
  const [cancellableLoader, image, selectList] = await Promise.all([
    import(pathToFileURL(join(piTuiRoot, "dist/components/cancellable-loader.js")).href),
    import(pathToFileURL(join(piTuiRoot, "dist/components/image.js")).href),
    import(pathToFileURL(join(piTuiRoot, "dist/components/select-list.js")).href),
  ]);
  return {
    CancellableLoader: cancellableLoader.CancellableLoader,
    Image: image.Image,
    SelectList: selectList.SelectList,
  };
};

export default function (pi: any) {
  pi.registerCommand("probe-clipboard-read", {
    description: "Parity harness: read clipboard image and report mime/bytes",
    handler: async (_args: string, ctx: any) => {
      await withAgentFixturePath(async () => {
        const { readClipboardImage } = await loadClipboardImageModule();
        const image = await readClipboardImage({
          platform: "linux",
          env: { ...process.env, WAYLAND_DISPLAY: process.env.WAYLAND_DISPLAY ?? "wayland-1", XDG_SESSION_TYPE: "wayland" },
        });
        if (!image) {
          ctx.ui.notify("clipboard:none", "info");
          return;
        }
        ctx.ui.notify(`clipboard:${image.mimeType}:${image.bytes.length}`, "info");
      });
    },
  });

  pi.registerCommand("probe-image-fallback", {
    description: "Parity harness: render Image fallback line",
    handler: async (_args: string, ctx: any) => {
      const { Image } = await loadTuiModules();
      const image = new Image(samplePngBase64, "image/png", { fallbackColor: (s: string) => s }, { filename: "probe.png" });
      const lines = image.render(80).filter((line: string) => line.length > 0);
      ctx.ui.notify(lines.join("\n"), "info");
    },
  });

  pi.registerCommand("probe-cancellable-loader", {
    description: "Parity harness: render CancellableLoader and cancel it",
    handler: async (_args: string, ctx: any) => {
      const { CancellableLoader } = await loadTuiModules();
      const loader = new CancellableLoader({ requestRender() {} } as any, (s: string) => s, (s: string) => s, "Working...");
      let onAbortCalled = false;
      loader.onAbort = () => {
        onAbortCalled = true;
      };
      const rendered = loader.render(40).join("\n");
      const renderOk = rendered.includes("Working...");
      loader.handleInput("\u001b");
      const aborted = loader.signal.aborted;
      loader.dispose();
      ctx.ui.notify(`cancellable-loader:render=${renderOk}:aborted=${aborted}:onAbort=${onAbortCalled}`, "info");
    },
  });

  pi.registerCommand("probe-select-list", {
    description: "Parity harness: exercise SelectList input boundaries",
    handler: async (_args: string, ctx: any) => {
      const { SelectList } = await loadTuiModules();
      const items = ["a", "b", "c"].map((value) => ({ value, label: value }));
      const theme = {
        selectedPrefix: (s: string) => s, selectedText: (s: string) => s,
        description: (s: string) => s, scrollInfo: (s: string) => s, noMatch: (s: string) => s,
      };
      const list = new SelectList(items, 3, theme);
      let changed = "";
      let selected = "";
      list.onSelectionChange = (item: any) => { changed = item.value; };
      list.onSelect = (item: any) => { selected = item.value; };
      list.setSelectedIndex(2);
      list.handleInput("\u001b[6~");
      const pageIgnored = changed === "";
      list.handleInput("\u001b[B");
      list.handleInput("\r");
      const cancelList = new SelectList(items, 3, theme);
      let cancelled = false;
      cancelList.onCancel = () => { cancelled = true; };
      cancelList.handleInput("\u001b");
      const renderOK = list.render(40).join("\n").includes("a");
      ctx.ui.notify(`select-list:render=${renderOK}:pageIgnored=${pageIgnored}:wrap=${changed}:select=${selected}:cancel=${cancelled}`, "info");
    },
  });
}
