// Pi modules resolve the same agent and project directories as the host.
// PiG-owned runtime caches remain under its separate product root.
import { homedir } from "node:os";
import { join, parse, sep } from "node:path";

// pig divergence (D2): sharing Pi state requires the host's explicit opt-in.
const usePiDirs = process.env.PIG_USE_PI_DIRS === "1";
export const CONFIG_DIR_NAME = usePiDirs ? ".pi" : ".pig";
// pig divergence (D2): imported Pi classes use the host's separate config tree.
export const APP_NAME = "pig";
export function getSessionsDir() {
  return join(getAgentDir(), "sessions");
}
export function getBinDir() { return join(getAgentDir(), "bin"); }
export const ENV_AGENT_DIR = usePiDirs ? "PI_CODING_AGENT_DIR" : "PIG_CODING_AGENT_DIR";

// internal/codingagent/paths.go ExpandTildePath.
function expandTildePath(path) {
  if (path === "~") return homedir();
  if (path.startsWith("~/")) return join(homedir(), path.slice(2));
  return path;
}

// The home directory as Go's os.UserHomeDir finds it: HOME (USERPROFILE on Windows), never the passwd database that os.homedir() falls back to.
function homeDirectory() {
  const home = process.env[process.platform === "win32" ? "USERPROFILE" : "HOME"];
  if (!home) throw new Error("locate home directory: home environment variable is not defined");
  return home;
}

// Go's filepath.Join as internal/configroot uses it: path.join, which already normalizes, without the trailing separator Go's Clean removes.
function joinClean(base, rest) {
  const joined = join(base, rest);
  return joined.length > parse(joined).root.length && joined.endsWith(sep) ? joined.slice(0, -1) : joined;
}

// internal/configroot ExpandHome: a leading ~ or ~/ is the home directory; every other path, including ~user, stays literal.
function expandHome(path) {
  if (path === "~") return homeDirectory();
  if (path.startsWith("~/")) return joinClean(homeDirectory(), path.slice(2));
  return path;
}

// internal/configroot Resolve: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig. An empty variable falls through to the next choice. It throws,
// and never returns a relative path, when the home directory is needed and cannot be found. Pi-sharing mode (PIG_USE_PI_DIRS=1) does not move it (D2).
export function getConfigRoot() {
  const pigHome = process.env.PIG_HOME;
  if (pigHome) return expandHome(pigHome);
  const xdgConfigHome = process.env.XDG_CONFIG_HOME;
  if (xdgConfigHome) return joinClean(expandHome(xdgConfigHome), "pig");
  return joinClean(homeDirectory(), ".pig");
}

export function getRuntimeCacheDir() {
  return join(getConfigRoot(), "cache");
}

export function getAgentDir() {
  const envDir = process.env[ENV_AGENT_DIR];
  if (envDir) return expandTildePath(envDir);
  if (usePiDirs) return join(homedir(), ".pi", "agent");
  return join(getConfigRoot(), "agent");
}
