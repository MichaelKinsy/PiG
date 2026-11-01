const JsonPointer = /^(?:\/(?:[^~/]|~0|~1)*)*$/;
/**
 * Returns true if the value is a json pointer
 * @specification https://datatracker.ietf.org/doc/html/rfc6901
 * @source https://github.com/ajv-validator/ajv-formats
 */
export function IsJsonPointer(value) {
    return JsonPointer.test(value);
}
