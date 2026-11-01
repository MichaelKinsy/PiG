/**
 * Returns true if the value is a regular expression string pattern
 * @specification https://ecma-international.org/ecma-262
 */
export function IsRegex(value) {
    try {
        new RegExp(value, 'u');
        return true;
    }
    catch {
        return false;
    }
}
