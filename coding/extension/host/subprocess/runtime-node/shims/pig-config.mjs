// Pi modules resolve the same agent and project directories as the host.
// PiG-owned runtime caches remain under its separate product root.
import { homedir } from "node:os";
import { join } from "node:path";

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

// internal/codingagent/paths.go ConfigRoot.
function configRoot() {
  const pigHome = process.env.PIG_HOME;
  if (pigHome) return expandTildePath(pigHome);
  const xdgConfigHome = process.env.XDG_CONFIG_HOME;
  if (xdgConfigHome) return join(expandTildePath(xdgConfigHome), "pig");
  return join(homedir(), ".pig");
}

export function getRuntimeCacheDir() {
  return join(configRoot(), "cache");
}

export function getAgentDir() {
  const envDir = process.env[ENV_AGENT_DIR];
  if (envDir) return expandTildePath(envDir);
  if (usePiDirs) return join(homedir(), ".pi", "agent");
  return join(configRoot(), "agent");
}
