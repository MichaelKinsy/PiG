const IPv4 = /^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;
/**
 * Returns true if the value is a IPV4 address
 * @specification http://tools.ietf.org/html/rfc2673#section-3.2
 */
export function IsIPv4(value) {
    return IPv4.test(value);
}
