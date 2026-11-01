/**
 * Returns true if the value is a valid Uniform Resource Locator
 * @specification https://datatracker.ietf.org/doc/html/rfc3986
 * @specification https://datatracker.ietf.org/doc/html/rfc3987
 */
export function IsUrl(value) {
    return URL.canParse(value);
}
