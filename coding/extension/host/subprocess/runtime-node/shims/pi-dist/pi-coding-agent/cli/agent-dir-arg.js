/**
 * `--agent-dir <dir>`, the command-line form of PI_CODING_AGENT_DIR. The flag is applied by setting the variable,
 * because pi, extensions (through the exported `getAgentDir()`) and child processes all read the agent dir from it.
 * main() applies it after all modules loaded: never cache the agent dir at module load (see config.ts).
 */
import { resolve } from "node:path";
import { ENV_AGENT_DIR, expandTildePath } from "../config.js";
/**
 * Take `--agent-dir <dir>` out of the arguments and set PI_CODING_AGENT_DIR to it, as an absolute path so the resume
 * command pi reports does not depend on the directory it runs in. Arguments after `--` are messages.
 */
export function applyAgentDirArg(args) {
    const result = takeAgentDirArg(args);
    if (result.kind === "ok" && result.agentDir) {
        process.env[ENV_AGENT_DIR] = resolve(expandTildePath(result.agentDir));
    }
    return result;
}
function takeAgentDirArg(args) {
    const rest = [];
    let agentDir;
    for (let i = 0; i < args.length; i++) {
        const arg = args[i];
        if (arg === "--") {
            rest.push(...args.slice(i));
            break;
        }
        if (arg !== "--agent-dir") {
            rest.push(arg);
            continue;
        }
        agentDir = args[++i];
        if (!agentDir)
            return { kind: "error", message: "--agent-dir requires a directory" };
    }
    return { kind: "ok", args: rest, agentDir };
}
//# sourceMappingURL=agent-dir-arg.js.map