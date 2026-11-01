import * as Pattern from '../pattern/pattern.mjs';
// -------------------------------------------------------------------
// Validates an ASCII LDH label per RFC 5891 §4.2.3.1
// -------------------------------------------------------------------
export function IsAsciiLabel(value) {
    return (Pattern.RE_RULE_HYPHEN_PLACEMENT.test(value) &&
        Pattern.RE_RULE_NOT_RESERVED_ACE.test(value) &&
        Pattern.RE_ASCII_LDH.test(value));
}
