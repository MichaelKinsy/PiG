function hexToRgb(hex) {
    const normalized = hex.startsWith("#") ? hex.slice(1) : hex;
    const r = parseInt(normalized.slice(0, 2), 16);
    const g = parseInt(normalized.slice(2, 4), 16);
    const b = parseInt(normalized.slice(4, 6), 16);
    return { r, g, b };
}
function parseOscHexChannel(channel) {
    if (!/^[0-9a-f]+$/i.test(channel)) {
        return undefined;
    }
    const max = 16 ** channel.length - 1;
    if (max <= 0) {
        return undefined;
    }
    return Math.round((parseInt(channel, 16) / max) * 255);
}
const OSC_COLOR_RESPONSE_PATTERN = /^\x1b\](?:(1[01])|4;(\d{1,3}));([^\x07\x1b]*)(?:\x07|\x1b\\)$/i;
const COLOR_SCHEME_REPORT_PATTERN = /^(?:\x1b\[\?997;(1|2)n)+$/;
/**
 * Parse an OSC 10, 11, or 4 color reply. Returns undefined when `data` is not such a reply;
 * `rgb` is undefined when it is a reply with an unparseable color.
 */
export function parseOscColorResponse(data) {
    const match = data.match(OSC_COLOR_RESPONSE_PATTERN);
    if (!match) {
        return undefined;
    }
    const target = match[1] === "10" ? "foreground" : match[1] === "11" ? "background" : Number.parseInt(match[2], 10);
    return { target, rgb: parseOscColorValue(match[3]) };
}
function parseOscColorValue(rawValue) {
    const value = rawValue.trim();
    if (value.startsWith("#")) {
        const hex = value.slice(1);
        if (/^[0-9a-f]{6}$/i.test(hex)) {
            return hexToRgb(value);
        }
        if (/^[0-9a-f]{12}$/i.test(hex)) {
            const r = parseOscHexChannel(hex.slice(0, 4));
            const g = parseOscHexChannel(hex.slice(4, 8));
            const b = parseOscHexChannel(hex.slice(8, 12));
            return r !== undefined && g !== undefined && b !== undefined ? { r, g, b } : undefined;
        }
        return undefined;
    }
    const rgbValue = value.replace(/^rgba?:/i, "");
    const [red, green, blue] = rgbValue.split("/");
    if (red === undefined || green === undefined || blue === undefined) {
        return undefined;
    }
    const r = parseOscHexChannel(red);
    const g = parseOscHexChannel(green);
    const b = parseOscHexChannel(blue);
    return r !== undefined && g !== undefined && b !== undefined ? { r, g, b } : undefined;
}
export function parseTerminalColorSchemeReport(data) {
    const match = data.match(COLOR_SCHEME_REPORT_PATTERN);
    if (!match) {
        return undefined;
    }
    return match[1] === "2" ? "light" : "dark";
}
//# sourceMappingURL=terminal-colors.js.map