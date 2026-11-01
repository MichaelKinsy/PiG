import * as FormatBidi from './format/bidi.mjs';
import * as LabelUnicode from './label/unicode.mjs';
import * as LabelPuny from './label/puny.mjs';
// ------------------------------------------------------------------
// IsLabel
// ------------------------------------------------------------------
function IsValidLabelLength(value) {
    return value.length > 0 && value.length <= 63;
}
function IsLabel(value) {
    return IsValidLabelLength(value) && (LabelPuny.IsPunyLabel(value) ||
        LabelUnicode.IsUnicodeLabel(value));
}
// ------------------------------------------------------------------
// NormalizeHostname
//
// Normalizes a hostname for label validation. Maps fullwidth
// codepoints to their ASCII equivalent (e.g. 'ａ' U+FF41 maps to
// 'a'), then normalizes for NFC such that equivalent codepoint
// sequences can be uniformly compared. It also removes codepoints
// that IDNA mapping ignores, and converts full stop variants to
// ASCII '.' so the hostname can be split into labels.
// ------------------------------------------------------------------
function NormalizeHostname(value) {
    return value
        .replace(/[\uff01-\uff5e]/g, (char) => String.fromCharCode(char.charCodeAt(0) - 0xfee0)) // RE_FULLWIDTH_MAPPING
        .normalize('NFC')
        .replace(/[\u00ad\u034f\u180b-\u180d\u200b\ufe00-\ufe0f\u{e0100}-\u{e01ef}]/gu, '') // RE_IGNORED_MAPPING
        .replace(/[\u002E\u3002\uFF0E\uFF61]/g, '.'); // RE_FULL_STOP_MAPPING
}
// ------------------------------------------------------------------
// IsIdnHostname
// ------------------------------------------------------------------
export function IsIdnHostname(value) {
    if (value.length === 0 || value.includes(' '))
        return false;
    const normalized = NormalizeHostname(value);
    if (normalized.length > 253)
        return false;
    const labels = normalized.split('.');
    const hasBidiChars = labels.some((label) => FormatBidi.HasBidiChars(label));
    return labels.every((label) => IsLabel(label) && (!hasBidiChars || FormatBidi.SatisfiesBidiRule(label)));
}
