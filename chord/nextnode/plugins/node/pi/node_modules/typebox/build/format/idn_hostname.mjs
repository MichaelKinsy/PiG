import * as Idna from './idna/index.mjs';
/**
 * Returns true if the value is a valid internationalized (IDN) hostname.
 * @specification https://tools.ietf.org/html/rfc3490
 * @specification https://tools.ietf.org/html/rfc5891
 * @specification https://tools.ietf.org/html/rfc5892
 * @specification https://tools.ietf.org/html/rfc5893
 */
export function IsIdnHostname(value) {
    return Idna.IsIdnHostname(value);
}
