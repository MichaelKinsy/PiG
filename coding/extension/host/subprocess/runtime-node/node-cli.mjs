// Node starts this file instead of `node --import register-loader.mjs cli.mjs`, so the loader hooks are registered by the process itself and process.execArgv stays what the user started Node with, as in Pi (an extension that forks Node with process.execArgv, or starts a worker thread, passes no PiG loader path on). The hooks must be in place before cli.mjs's module graph is resolved, which is why cli.mjs is imported dynamically after the registration; argv stays [node, this file, <extension entry>] as it was for cli.mjs.
import "./register-loader.mjs";
await import("./cli.mjs");
