import { join } from "node:path";
import { getAgentDir } from "../config.js";
import { resolvePath } from "../utils/paths.js";
import { ModelRuntime } from "./model-runtime.js";
import { DefaultResourceLoader, } from "./resource-loader.js";
import { createAgentSession } from "../../../independent-session.mjs";
import { SettingsManager } from "./settings-manager.js";
function applyExtensionFlagValues(resourceLoader, extensionFlagValues) {
    if (!extensionFlagValues) {
        return [];
    }
    const diagnostics = [];
    const extensionsResult = resourceLoader.getExtensions();
    const registeredFlags = new Map();
    for (const extension of extensionsResult.extensions) {
        for (const [name, flag] of extension.flags) {
            registeredFlags.set(name, { type: flag.type });
        }
    }
    const unknownFlags = [];
    for (const [name, value] of extensionFlagValues) {
        const flag = registeredFlags.get(name);
        if (!flag) {
            unknownFlags.push(name);
            continue;
        }
        if (flag.type === "boolean") {
            extensionsResult.runtime.flagValues.set(name, true);
            continue;
        }
        if (typeof value === "string") {
            extensionsResult.runtime.flagValues.set(name, value);
            continue;
        }
        diagnostics.push({
            type: "error",
            message: `Extension flag "--${name}" requires a value`,
        });
    }
    if (unknownFlags.length > 0) {
        diagnostics.push({
            type: "error",
            message: `Unknown option${unknownFlags.length === 1 ? "" : "s"}: ${unknownFlags.map((name) => `--${name}`).join(", ")}`,
        });
    }
    return diagnostics;
}
/**
 * Create cwd-bound runtime services.
 *
 * Returns services plus diagnostics. It does not create an AgentSession.
 */
export async function createAgentSessionServices(options) {
    const cwd = resolvePath(options.cwd);
    const agentDir = options.agentDir ? resolvePath(options.agentDir) : getAgentDir();
    const modelRuntime = options.modelRuntime ??
        (await ModelRuntime.create({
            authPath: join(agentDir, "auth.json"),
            modelsPath: join(agentDir, "models.json"),
            signal: options.modelRuntimeSignal,
        }));
    const settingsManager = options.settingsManager ?? SettingsManager.create(cwd, agentDir);
    const resourceLoader = new DefaultResourceLoader({
        ...(options.resourceLoaderOptions ?? {}),
        cwd,
        agentDir,
        settingsManager,
    });
    await resourceLoader.reload(options.resourceLoaderReloadOptions);
    const diagnostics = [];
    const extensionsResult = resourceLoader.getExtensions();
    for (const { name, config, extensionPath } of extensionsResult.runtime.pendingProviderRegistrations) {
        try {
            modelRuntime.registerProvider(name, config);
        }
        catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            diagnostics.push({
                type: "error",
                message: `Extension "${extensionPath}" error: ${message}`,
            });
        }
    }
    extensionsResult.runtime.pendingProviderRegistrations = [];
    for (const { provider, extensionPath } of extensionsResult.runtime.pendingNativeProviderRegistrations) {
        try {
            modelRuntime.registerNativeProvider(provider);
        }
        catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            diagnostics.push({
                type: "error",
                message: `Extension "${extensionPath}" error: ${message}`,
            });
        }
    }
    extensionsResult.runtime.pendingNativeProviderRegistrations = [];
    for (const { definition, extensionPath } of extensionsResult.runtime.pendingVirtualModelRegistrations) {
        try {
            modelRuntime.registerVirtualModel(definition);
        }
        catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            diagnostics.push({
                type: "error",
                message: `Extension "${extensionPath}" error: ${message}`,
            });
        }
    }
    extensionsResult.runtime.pendingVirtualModelRegistrations = [];
    await modelRuntime.refresh({ allowNetwork: false });
    diagnostics.push(...applyExtensionFlagValues(resourceLoader, options.extensionFlagValues));
    return {
        cwd,
        agentDir,
        modelRuntime,
        settingsManager,
        resourceLoader,
        diagnostics,
    };
}
/**
 * Create an AgentSession from previously created services.
 *
 * This keeps session creation separate from service creation so callers can
 * resolve model, thinking, tools, and other session inputs against the target
 * cwd before constructing the session.
 */
export async function createAgentSessionFromServices(options) {
    return createAgentSession({
        cwd: options.services.cwd,
        agentDir: options.services.agentDir,
        modelRuntime: options.services.modelRuntime,
        settingsManager: options.services.settingsManager,
        resourceLoader: options.services.resourceLoader,
        sessionManager: options.sessionManager,
        model: options.model,
        thinkingLevel: options.thinkingLevel,
        scopedModels: options.scopedModels,
        tools: options.tools,
        excludeTools: options.excludeTools,
        noTools: options.noTools,
        customTools: options.customTools,
        sessionStartEvent: options.sessionStartEvent,
    });
}
//# sourceMappingURL=agent-session-services.js.map