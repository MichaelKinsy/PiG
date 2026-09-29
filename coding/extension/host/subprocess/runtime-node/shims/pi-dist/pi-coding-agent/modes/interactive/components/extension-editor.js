/**
 * Multi-line editor component for extensions.
 * Supports Ctrl+G for external editor.
 */
import { Container, Editor, getKeybindings, Spacer, Text, } from "../../../../../pi-tui.mjs";
import { editInExternalEditor } from "../external-editor.js";
import { getEditorTheme, theme } from "../theme/theme.js";
import { DynamicBorder } from "./dynamic-border.js";
import { keyHint } from "./keybinding-hints.js";
export class ExtensionEditorComponent extends Container {
    editor;
    onSubmitCallback;
    onCancelCallback;
    tui;
    keybindings;
    externalEditorCommand;
    _focused = false;
    get focused() {
        return this._focused;
    }
    set focused(value) {
        this._focused = value;
        this.editor.focused = value;
    }
    constructor(tui, keybindings, title, prefill, onSubmit, onCancel, options, externalEditorCommand) {
        super();
        this.tui = tui;
        this.keybindings = keybindings;
        this.externalEditorCommand =
            externalEditorCommand ||
                process.env.VISUAL ||
                process.env.EDITOR ||
                (process.platform === "win32" ? "notepad" : "nano");
        this.onSubmitCallback = onSubmit;
        this.onCancelCallback = onCancel;
        const { description, ...editorOptions } = options ?? {};
        // Add top border
        this.addChild(new DynamicBorder());
        this.addChild(new Spacer(1));
        // Add title and optional description
        this.addChild(new Text(theme.fg("accent", title), 1, 0));
        if (description) {
            this.addChild(new Spacer(1));
            this.addChild(new Text(theme.fg("text", description), 1, 0));
        }
        this.addChild(new Spacer(1));
        // Create editor
        this.editor = new Editor(tui, getEditorTheme(), editorOptions);
        if (prefill) {
            this.editor.setText(prefill);
        }
        // Wire up Enter to submit (Shift+Enter for newlines, like the main editor)
        this.editor.onSubmit = (text) => {
            this.onSubmitCallback(text);
        };
        this.addChild(this.editor);
        this.addChild(new Spacer(1));
        // Add hint
        const hint = keyHint("tui.select.confirm", "submit") +
            "  " +
            keyHint("tui.input.newLine", "newline") +
            "  " +
            keyHint("tui.select.cancel", "cancel") +
            `  ${keyHint("app.editor.external", "external editor")}`;
        this.addChild(new Text(hint, 1, 0));
        this.addChild(new Spacer(1));
        // Add bottom border
        this.addChild(new DynamicBorder());
    }
    handleInput(keyData) {
        const kb = getKeybindings();
        // Escape or Ctrl+C to cancel
        if (kb.matches(keyData, "tui.select.cancel")) {
            this.onCancelCallback();
            return;
        }
        // External editor (app keybinding)
        if (this.keybindings.matches(keyData, "app.editor.external")) {
            void this.handleOpenExternalEditor();
            return;
        }
        // Forward to editor
        this.editor.handleInput(keyData);
    }
    async handleOpenExternalEditor() {
        const content = this.editor.getText();
        this.tui.stop();
        try {
            const result = await editInExternalEditor({
                command: this.externalEditorCommand,
                content,
            });
            if (result.status === "complete") {
                this.editor.setText(result.content);
            }
        }
        finally {
            this.tui.start();
            this.tui.requestRender(true);
        }
    }
}
//# sourceMappingURL=extension-editor.js.map