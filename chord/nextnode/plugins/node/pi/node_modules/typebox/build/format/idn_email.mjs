const IdnEmail = /^(?:[A-Za-z0-9!#$%&'*+\/=?^_`{|}~\u{0080}-\u{10FFFF}-]+(?:\.[A-Za-z0-9!#$%&'*+\/=?^_`{|}~\u{0080}-\u{10FFFF}-]+)*|"(?:[^"\\]|\\.)*")@[\p{L}\p{N}](?:[\p{L}\p{N}-]{0,62})(?<!-)(?:\.[\p{L}\p{N}](?:[\p{L}\p{N}-]{0,62})(?<!-))*$/iu;
/**
 * Returns true if the value is an IdnEmail
 * @specification https://datatracker.ietf.org/doc/html/rfc4952
 */
export function IsIdnEmail(value) {
    return IdnEmail.test(value.normalize('NFC'));
}
