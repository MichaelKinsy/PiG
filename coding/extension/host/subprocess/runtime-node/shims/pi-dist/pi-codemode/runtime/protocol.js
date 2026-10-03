export function isWorkerToHostMessage(value) {
    if (typeof value !== "object" || value === null)
        return false;
    const type = value.type;
    return type === "call" || type === "output" || type === "done" || type === "crash";
}
export function isHostToWorkerMessage(value) {
    return typeof value === "object" && value !== null && value.type === "result";
}
//# sourceMappingURL=protocol.js.map