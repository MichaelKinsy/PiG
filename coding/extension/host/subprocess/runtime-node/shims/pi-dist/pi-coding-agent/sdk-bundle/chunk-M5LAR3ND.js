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
      total: first.cost.total + second.cost.total
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

export {
  createUsageTotals,
  addUsageToTotals,
  combineUsage,
  getUsageCostBreakdown
};
