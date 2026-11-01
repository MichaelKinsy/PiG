// deno-lint-ignore-file no-control-regex
const IpvFutureMatchMaxLength = 2048;
const IpvFutureMatch = /\[[vV][0-9a-fA-F]+\.[^\]]+\]/; // Guarded By IpvFutureMatchMaxLength
const InvalidIriChars = /[\x00-\x20<>\^`{|}\\]/;
const InvalidPercentEncoding = /%(?![0-9a-fA-F]{2})/;
// ------------------------------------------------------------------
// NarrowIpvFuture
//
// Substitutes an IPvFuture address with a standard IPv6 loopback
// address ([::1]). We do this because the native URL.canParse
// (WHATWG standard) rejects IPvFuture literals, which are
// otherwise valid in RFC 3987. Because regex substitution can
// be expensive on large strings, this operation is strictly
// limited to inputs under a defined length threshold.
//
// (review-optimization)
//
// ------------------------------------------------------------------
function NarrowIpvFuture(value) {
    return value.length < IpvFutureMatchMaxLength ? value.replace(IpvFutureMatch, '[::1]') : value;
}
/**
 * Returns true if the value is a valid Internationalized Resource Identifier.
 * @specification https://datatracker.ietf.org/doc/html/rfc3987
 */
export function IsIri(value) {
    // 1. Reject strings containing unencoded whitespace or illegal control characters.
    if (InvalidIriChars.test(value))
        return false;
    // 2. Reject malformed percent-encoding triplets.
    if (InvalidPercentEncoding.test(value))
        return false;
    // 3. Delegate to the native URL parser, patching the IPvFuture edge case beforehand.
    return URL.canParse(NarrowIpvFuture(value));
}
