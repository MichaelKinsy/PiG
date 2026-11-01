import * as LabelAscii from './label/ascii.mjs';
import * as LabelPuny from './label/puny.mjs';
function IsValidLabelLength(value) {
    return value.length > 0 && value.length <= 63;
}
function IsLabel(value) {
    return IsValidLabelLength(value) && (LabelPuny.IsPunyLabel(value) ||
        LabelAscii.IsAsciiLabel(value));
}
export function IsHostname(value) {
    if (value.length === 0 || value.length > 253)
        return false;
    if (value.charCodeAt(value.length - 1) === 46)
        return false;
    return value.split('.').every((label) => IsLabel(label));
}
