import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/core/usage-totals.js
function createUsageTotals() {
  return {
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheWrite: 0,
    cost: 0
  };
}
__name(createUsageTotals, "createUsageTotals");
function addUsageToTotals(totals, usage) {
  totals.input += usage.input;
  totals.output += usage.output;
  totals.cacheRead += usage.cacheRead;
  totals.cacheWrite += usage.cacheWrite;
  totals.cost += usage.cost.total;
}
__name(addUsageToTotals, "addUsageToTotals");
function combineUsage(first, second) {
  return {
    input: first.input + second.input,
    output: first.output + second.output,
    cacheRead: first.cacheRead + second.cacheRead,
    cacheWrite: first.cacheWrite + second.cacheWrite,
    ...first.cacheWrite1h !== void 0 || second.cacheWrite1h !== void 0 ? { cacheWrite1h: (first.cacheWrite1h ?? 0) + (second.cacheWrite1h ?? 0) } : {},
    ...first.reasoning !== void 0 || second.reasoning !== void 0 ? { reasoning: (first.reasoning ?? 0) + (second.reasoning ?? 0) } : {},
    totalTokens: first.totalTokens + second.totalTokens,
    cost: {
      input: first.cost.input + second.cost.input,
      output: first.cost.output + second.cost.output,
      cacheRead: first.cost.cacheRead + second.cost.cacheRead,
      cacheWrite: first.cost.cacheWrite + second.cost.cacheWrite,
      total: first.cost.total + second.cost.total,
      // A sum is provider-reported only when every part is.
      totalFromProvider: first.cost.totalFromProvider === true && second.cost.totalFromProvider === true
    }
  };
}
__name(combineUsage, "combineUsage");
function getUsageCostBreakdown(entries) {
  const totalsByKey = /* @__PURE__ */ new Map();
  for (const entry of entries) {
    let key;
    let usage;
    if (entry.type === "message" && entry.message.role === "assistant") {
      key = `${entry.message.provider}/${entry.message.responseModel ?? entry.message.model}`;
      usage = entry.message.usage;
    } else if (entry.type === "usage") {
      key = `${entry.provider}/${entry.model}`;
      usage = entry.usage;
    } else if (entry.type === "message" && entry.message.role === "toolResult" && entry.message.usage) {
      key = "Tools/summaries";
      usage = entry.message.usage;
    } else if ((entry.type === "branch_summary" || entry.type === "compaction") && entry.usage) {
      key = "Tools/summaries";
      usage = entry.usage;
    }
    if (!key || !usage)
      continue;
    let totals = totalsByKey.get(key);
    if (!totals) {
      totals = createUsageTotals();
      totalsByKey.set(key, totals);
    }
    addUsageToTotals(totals, usage);
  }
  return Array.from(totalsByKey, ([key, totals]) => ({
    key,
    cost: totals.cost,
    tokens: totals.input + totals.output + totals.cacheRead + totals.cacheWrite
  })).filter((entry) => entry.cost > 0 || entry.tokens > 0).sort((a, b) => b.cost - a.cost);
}
__name(getUsageCostBreakdown, "getUsageCostBreakdown");

// pi-dist/pi-coding-agent/core/settings-defaults.js
import { DEFAULT_MAX_AGENT_RETRY_DELAY_MS } from "../../pi-ai/utils/retry.js";
var SETTINGS_DEFAULTS = {
  transport: "auto",
  sessionShareDestination: "auto",
  steeringMode: "one-at-a-time",
  followUpMode: "one-at-a-time",
  compaction: {
    enabled: true,
    reserveTokens: 16384,
    keepRecentTokens: 2e4
  },
  branchSummary: {
    reserveTokens: 16384,
    skipPrompt: false
  },
  retry: {
    enabled: true,
    maxRetries: 3,
    baseDelayMs: 2e3,
    maxAgentDelayMs: DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
    provider: {
      maxRetryDelayMs: 6e4
    }
  },
  hideThinkingBlock: false,
  showCacheMissNotices: false,
  quietStartup: false,
  defaultProjectTrust: "ask",
  collapseChangelog: false,
  enableInstallTelemetry: true,
  enableAnalytics: false,
  enableSkillCommands: true,
  terminal: {
    showImages: true,
    imageWidthCells: 60,
    clearOnShrink: false,
    showTerminalProgress: false
  },
  images: {
    autoResize: true,
    blockImages: false
  },
  doubleEscapeAction: "tree",
  treeFilterMode: "default",
  editorPaddingX: 0,
  outputPad: 1,
  autocompleteMaxVisible: 5,
  markdown: {
    codeBlockIndent: "  ",
    mermaid: "streaming"
  },
  warnings: {
    anthropicExtraUsage: true
  },
  codemode: {
    mode: "on",
    inlineBudget: 3e3
  },
  cacheWarming: "streaming",
  tuiMode: "fullscreen",
  fullscreenExitOutput: "transcript",
  fullscreenScrollbar: "auto",
  fullscreenCopyOnSelect: true,
  fullscreenWheelScrollLines: "auto"
};

export {
  SETTINGS_DEFAULTS,
  createUsageTotals,
  addUsageToTotals,
  combineUsage,
  getUsageCostBreakdown
};
