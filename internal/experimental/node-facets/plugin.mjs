// Ports packages/coding-agent/src/experimental/plugin.ts and its runtime service-token exports.
import { defineService } from "./chord/index.js";

export const AgentController = defineService("pi.agent-controller");
export const PresentationUI = defineService("pi.local.presentation-ui", { local: true });
export const SlashCommands = defineService("pi.local.slash-commands", { local: true });
