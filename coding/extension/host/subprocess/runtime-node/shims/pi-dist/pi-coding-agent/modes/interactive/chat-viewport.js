import { ScrollView, VStack } from "../../../../pi-tui.mjs";
/** Shared fullscreen transcript and fixed input-dock layout. */
export function createChatViewport(options) {
    const transcript = new ScrollView(options.document, {
        follow: "end",
        primary: true,
        overscroll: "chain",
        scrollbar: options.scrollbar ?? "auto",
        ...(options.scrollbarTrackStyle === undefined ? {} : { scrollbarTrackStyle: options.scrollbarTrackStyle }),
        ...(options.scrollbarThumbStyle === undefined ? {} : { scrollbarThumbStyle: options.scrollbarThumbStyle }),
    });
    const dock = new VStack([
        { component: options.pendingMessages, shrink: 1, minSize: 0 },
        { component: options.status, shrink: 1, minSize: 0 },
        ...(options.widgetsAbove === undefined ? [] : [{ component: options.widgetsAbove, shrink: 1, minSize: 0 }]),
        { component: options.editor, shrink: 1, minSize: 3 },
        ...(options.widgetsBelow === undefined ? [] : [{ component: options.widgetsBelow, shrink: 1, minSize: 0 }]),
        { component: options.footer, shrink: 1, minSize: 0 },
    ]);
    return {
        transcript,
        root: new VStack([
            { component: transcript, basis: 0, grow: 1, shrink: 1, minSize: 1 },
            { component: dock, basis: "auto", grow: 0, shrink: 1, minSize: 1 },
        ]),
    };
}
//# sourceMappingURL=chat-viewport.js.map