// The packed cell's counterpart of node-cli.mjs: registers the runtime's loader hooks, then runs cell.mjs with argv [node, this file, <manifest>, <entries>...], so process.execArgv is what the user started Node with (issue #517).
import "./register-loader.mjs";
await import("./cell.mjs");
