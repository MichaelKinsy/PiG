import * as FormatPuny from '../format/puny.mjs';
import * as LabelUnicode from './unicode.mjs';
import * as Pattern from '../pattern/pattern.mjs';
// ------------------------------------------------------------------
// IsPunyLabel
// ------------------------------------------------------------------
export function IsPunyLabel(value) {
    if (!FormatPuny.IsAcePrefixed(value))
        return false;
    try {
        const body = value.slice(4).toLowerCase();
        // A payload consisting of only a hyphen, or starting with the delimiter
        // (like "-9uc"), has 0 basic ASCII characters preceding it and is malformed.
        if (body.lastIndexOf('-') === 0)
            return false;
        const decoded = FormatPuny.Decode(body);
        // RFC 5890 §2.3.2.1 - a U-label must contain at least one non-ASCII
        // character. Code-unit level check: any non-ASCII codepoint has at least
        // one UTF-16 unit >= 0x80 (surrogate halves included), so no need to
        // decode codepoints or allocate a character array to answer this.
        if (!Pattern.RE_NON_ASCII.test(decoded))
            return false;
        return LabelUnicode.IsUnicodeLabel(decoded);
    }
    catch {
        return false;
    }
}
