export const REMOTE_SERVICE_ERROR_CODES = [
    "service_not_allowed",
    "service_not_found",
    "service_mode_mismatch",
    "service_member_not_found",
    "service_member_mismatch",
    "service_instance_not_found",
    "service_stale_instance",
    "service_invalid_value",
];
export function isRemoteServiceErrorCode(value) {
    return typeof value === "string" && REMOTE_SERVICE_ERROR_CODES.includes(value);
}
export class RemoteServiceError extends Error {
    code;
    constructor(code, message) {
        super(message);
        this.name = "RemoteServiceError";
        this.code = code;
    }
}
//# sourceMappingURL=errors.js.map