#!/usr/bin/env node
// The Node standalone of the Nix acceptance check: it records the interpreter that runs it and exits.
import { writeFileSync } from "node:fs";

writeFileSync(process.env.PIG_NIX_NODE_PROBE, process.execPath);
