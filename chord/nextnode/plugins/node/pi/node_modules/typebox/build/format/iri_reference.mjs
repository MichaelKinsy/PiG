// deno-lint-ignore-file no-control-regex
// Rejects unencoded spaces (0x20), backslashes, control characters (0x00-0x1F, 0x7F), or malformed percent-encodings
const InvalidIriChars = /[\x00-\x20\x7F\\]|%(?![0-9a-fA-F]{2})/;
// Detects malformed scheme/authority patterns lacking a colon (e.g. "httpx//example.com")
const MalformedScheme = /^[a-zA-Z][a-zA-Z0-9+\-.]*\/\//;
/**
 * Returns true if the value is an IRI reference
 * @specification https://tools.ietf.org/html/rfc3987
 */
export function IsIriReference(value) {
    return (!InvalidIriChars.test(value) &&
        !MalformedScheme.test(value) &&
        URL.canParse(value, 'http://example.com'));
}
