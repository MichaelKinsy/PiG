const RelativeJsonPointer = /^(?:0|[1-9][0-9]*)(?:#|(?:\/(?:[^~/]|~0|~1)*)*)$/;
/**
 * Returns true if the value is a relative json pointer
 * @specification https://datatracker.ietf.org/doc/html/rfc6901
 * @source https://github.com/ajv-validator/ajv-formats
 */
export function IsRelativeJsonPointer(value) {
    return RelativeJsonPointer.test(value);
}
