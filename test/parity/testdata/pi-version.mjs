// The Pi package version every oracle script's version guard compares the installed Pi with, from the one place it is
// pinned: .upstream/pin's package_version, or PIG_UPSTREAM_PACKAGE_VERSION, which `automation/pin/pin.py env` exports
// (for a PI_UPSTREAM_REF override too). A pin move changes no script.
import { readFileSync } from "node:fs";

const pinned = /^package_version=(\S+)$/m.exec(readFileSync(new URL("../../../.upstream/pin", import.meta.url), "utf8"));
if (pinned === null) throw new Error(".upstream/pin has no package_version");

export const PI_PACKAGE_VERSION = process.env.PIG_UPSTREAM_PACKAGE_VERSION || pinned[1];
