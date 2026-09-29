const ESC = String.fromCharCode(27);
/** Dim frame, plain labels, cyan connectors. Readable on light and dark. */
export const DEFAULT_THEME = {
    border: '2',
    edge: '36',
    edgeLabel: '2;36',
    title: '1',
};
/**
 * Render art to ANSI-coloured lines.
 *
 * A convenience over mapping `art.styled` yourself — reach for that directly
 * when your TUI has its own styling model.
 */
export function toAnsi(art, theme = DEFAULT_THEME) {
    return art.styled.map((row) => row
        .map((span) => {
        const sgr = theme[span.cls];
        return sgr === undefined ? span.text : `${ESC}[${sgr}m${span.text}${ESC}[0m`;
    })
        .join(''));
}
//# sourceMappingURL=ansi.js.map