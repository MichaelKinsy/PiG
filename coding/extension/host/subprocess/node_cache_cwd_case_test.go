package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi 1be312b4 (#11588) loader.ts useExtensionCacheCwd: the extension factory cache is cleared when the configured cwd changes, and "changes"
// is !pathsEqual(extensionCacheCwd, resolvedCwd). pathsEqual compares getPathComparisonKey (utils/paths.ts): the platform's resolved path,
// lower-cased on Windows only, with the platform read at call time so tests can simulate Windows. A session replacement whose cwd differs
// from the first only in case (`c:\work\proj` against `C:\Work\Proj`) is the same directory on Windows and keeps the cache; on a
// case-sensitive platform it is another directory and clears it. The script runs Generations.prepareCache, the port of useExtensionCacheCwd,
// with process.platform set as Pi's tests set it.
func TestPrepareCacheComparesTheCwdAsPiDoesOnWindows(t *testing.T) {
	nodeCellRequireNode(t)
	generationsPath, err := filepath.Abs("runtime-node/generations.mjs")
	if err != nil {
		t.Fatal(err)
	}
	const script = `
import { pathToFileURL } from "node:url";
const { Generations } = await import(pathToFileURL(process.argv[1]).href);
const realPlatform = process.platform;
const cases = [
  ["windows, cwd differing only in case", "win32", "C:\\Work\\Proj", "c:\\work\\proj", true],
  ["windows, another directory", "win32", "C:\\Work\\Proj", "C:\\Work\\Other", false],
  ["case-sensitive platform, cwd differing only in case", realPlatform === "win32" ? "linux" : realPlatform, "/work/Proj", "/work/proj", false],
  ["case-sensitive platform, same cwd", realPlatform === "win32" ? "linux" : realPlatform, "/work/Proj", "/work/Proj", true],
];
let failed = false;
for (const [name, platform, first, second, kept] of cases) {
  Object.defineProperty(process, "platform", { value: platform });
  const generations = new Generations(0);
  generations.prepareCache({ cwd: first });
  generations.factories.set("entry", () => {});
  generations.prepareCache({ cwd: second });
  const got = generations.factories.has("entry");
  if (got !== kept) { console.log(name + ": cache kept = " + got + ", want " + kept); failed = true; }
}
Object.defineProperty(process, "platform", { value: realPlatform });
process.exit(failed ? 1 : 0);
`
	cmd := exec.CommandContext(testbudget.Context(t), "node", "--input-type=module", "--eval", script, generationsPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prepareCache: %v\n%s", err, output)
	}
}
