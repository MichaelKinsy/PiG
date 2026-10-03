import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/core/tools/tool-definition-wrapper.js
function wrapToolDefinition(definition, ctxFactory) {
  return {
    name: definition.name,
    label: definition.label,
    description: definition.description,
    parameters: definition.parameters,
    outputSchema: definition.outputSchema,
    constrainedSampling: definition.constrainedSampling,
    prepareArguments: definition.prepareArguments,
    executionMode: definition.executionMode,
    execute: /* @__PURE__ */ __name((toolCallId, params, signal, onUpdate, ctx) => definition.execute(toolCallId, params, signal, onUpdate, ctx ?? ctxFactory?.(toolCallId, signal)), "execute")
  };
}
__name(wrapToolDefinition, "wrapToolDefinition");
function createToolDefinitionFromAgentTool(tool) {
  return {
    name: tool.name,
    label: tool.label,
    description: tool.description,
    parameters: tool.parameters,
    outputSchema: tool.outputSchema,
    constrainedSampling: tool.constrainedSampling,
    prepareArguments: tool.prepareArguments,
    executionMode: tool.executionMode,
    execute: /* @__PURE__ */ __name(async (toolCallId, params, signal, onUpdate) => tool.execute(toolCallId, params, signal, onUpdate), "execute")
  };
}
__name(createToolDefinitionFromAgentTool, "createToolDefinitionFromAgentTool");

export {
  wrapToolDefinition,
  createToolDefinitionFromAgentTool
};
