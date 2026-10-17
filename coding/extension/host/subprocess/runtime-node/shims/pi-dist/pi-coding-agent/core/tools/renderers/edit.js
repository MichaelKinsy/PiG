/**
 * Presentation for the edit tool.
 *
 * Renderers live apart from the implementation so a process that only displays tool output does not
 * load the execution path or its typebox parameter schema. `edit.ts` spreads these into its
 * definition, so the tool's public shape is unchanged.
 */
import { Box, Container, Spacer, Text } from "../../../../../pi-tui.mjs";
import { renderDiff } from "../../../modes/interactive/components/diff.js";
import { computeEditsDiff } from "../edit-diff.js";
import { renderToolPath, str } from "../render-utils.js";
function createEditCallRenderComponent() {
    return Object.assign(new Box(1, 1, (text) => text), {
        preview: undefined,
        previewArgsKey: undefined,
        previewPending: false,
        settledError: false,
    });
}
function getEditCallRenderComponent(state, lastComponent) {
    if (lastComponent instanceof Box) {
        const component = lastComponent;
        state.callComponent = component;
        return component;
    }
    if (state.callComponent) {
        return state.callComponent;
    }
    const component = createEditCallRenderComponent();
    state.callComponent = component;
    return component;
}
function getRenderablePreviewInput(args) {
    if (!args) {
        return null;
    }
    const path = typeof args.path === "string" ? args.path : typeof args.file_path === "string" ? args.file_path : null;
    if (!path) {
        return null;
    }
    if (Array.isArray(args.edits) &&
        args.edits.length > 0 &&
        args.edits.every((edit) => typeof edit?.oldText === "string" && typeof edit?.newText === "string")) {
        return { path, edits: args.edits };
    }
    if (typeof args.oldText === "string" && typeof args.newText === "string") {
        return { path, edits: [{ oldText: args.oldText, newText: args.newText }] };
    }
    return null;
}
function formatEditCall(args, theme, cwd) {
    const pathDisplay = renderToolPath(str(args?.file_path ?? args?.path), theme, cwd);
    return `${theme.fg("toolTitle", theme.bold("edit"))} ${pathDisplay}`;
}
function formatEditResult(args, preview, result, theme, isError) {
    const rawPath = str(args?.file_path ?? args?.path);
    const previewDiff = preview && !("error" in preview) ? preview.diff : undefined;
    const previewError = preview && "error" in preview ? preview.error : undefined;
    if (isError) {
        const errorText = result.content
            .filter((c) => c.type === "text")
            .map((c) => c.text || "")
            .join("\n");
        if (!errorText || errorText === previewError) {
            return undefined;
        }
        return theme.fg("error", errorText);
    }
    const resultDiff = result.details?.diff;
    if (resultDiff && resultDiff !== previewDiff) {
        return renderDiff(resultDiff, { filePath: rawPath ?? undefined });
    }
    return undefined;
}
function getEditHeaderBg(preview, settledError, theme) {
    if (preview) {
        if ("error" in preview) {
            return (text) => theme.bg("toolErrorBg", text);
        }
        return (text) => theme.bg("toolSuccessBg", text);
    }
    if (settledError) {
        return (text) => theme.bg("toolErrorBg", text);
    }
    return (text) => theme.bg("toolPendingBg", text);
}
function buildEditCallComponent(component, args, theme, cwd, outputPad) {
    component.setBgFn(getEditHeaderBg(component.preview, component.settledError, theme));
    component.setPaddingX(outputPad);
    component.clear();
    component.addChild(new Text(formatEditCall(args, theme, cwd), 0, 0));
    if (!component.preview) {
        return component;
    }
    const body = "error" in component.preview ? theme.fg("error", component.preview.error) : renderDiff(component.preview.diff);
    component.addChild(new Spacer(1));
    component.addChild(new Text(body, 0, 0));
    return component;
}
function setEditPreview(component, preview, argsKey) {
    const current = component.preview;
    const changed = current === undefined ||
        ("error" in current && "error" in preview
            ? current.error !== preview.error
            : "error" in current !== "error" in preview) ||
        (!("error" in current) &&
            !("error" in preview) &&
            (current.diff !== preview.diff || current.firstChangedLine !== preview.firstChangedLine));
    component.preview = preview;
    component.previewArgsKey = argsKey;
    component.previewPending = false;
    return changed;
}
export const editRenderers = {
    renderCall(args, theme, context) {
        const component = getEditCallRenderComponent(context.state, context.lastComponent);
        const previewInput = getRenderablePreviewInput(args);
        const argsKey = previewInput ? JSON.stringify({ path: previewInput.path, edits: previewInput.edits }) : undefined;
        if (component.previewArgsKey !== argsKey) {
            component.preview = undefined;
            component.previewArgsKey = argsKey;
            component.previewPending = false;
            component.settledError = false;
        }
        if (context.argsComplete && previewInput && !component.preview && !component.previewPending) {
            component.previewPending = true;
            const requestKey = argsKey;
            void computeEditsDiff(previewInput.path, previewInput.edits, context.cwd).then((preview) => {
                if (component.previewArgsKey === requestKey) {
                    setEditPreview(component, preview, requestKey);
                    context.invalidate();
                }
            });
        }
        return buildEditCallComponent(component, args, theme, context.cwd, context.outputPad);
    },
    renderResult(result, _options, theme, context) {
        const callComponent = context.state.callComponent;
        const previewInput = getRenderablePreviewInput(context.args);
        const argsKey = previewInput ? JSON.stringify({ path: previewInput.path, edits: previewInput.edits }) : undefined;
        const typedResult = result;
        const resultDiff = !context.isError ? typedResult.details?.diff : undefined;
        let changed = false;
        if (callComponent) {
            if (typeof resultDiff === "string") {
                changed =
                    setEditPreview(callComponent, { diff: resultDiff, firstChangedLine: typedResult.details?.firstChangedLine }, argsKey) || changed;
            }
            if (callComponent.settledError !== context.isError) {
                callComponent.settledError = context.isError;
                changed = true;
            }
            if (changed) {
                buildEditCallComponent(callComponent, context.args, theme, context.cwd, context.outputPad);
            }
        }
        const output = formatEditResult(context.args, callComponent?.preview, typedResult, theme, context.isError);
        const component = context.lastComponent ?? new Container();
        component.clear();
        if (!output) {
            return component;
        }
        component.addChild(new Spacer(1));
        component.addChild(new Text(output, context.outputPad, 0));
        return component;
    },
};
//# sourceMappingURL=edit.js.map