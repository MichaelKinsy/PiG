import { parentPort } from "node:worker_threads";
import { IMAGE_RESIZE_WORKER_RESPONSE_TYPE, resizeImageInProcess, } from "./image-resize-core.js";
function isResizeImageWorkerRequest(value) {
    if (!value || typeof value !== "object")
        return false;
    const record = value;
    return record.inputBytes instanceof Uint8Array && typeof record.mimeType === "string";
}
const port = parentPort;
if (!port) {
    throw new Error("image resize worker requires parentPort");
}
port.once("message", (message) => {
    void (async () => {
        try {
            if (!isResizeImageWorkerRequest(message)) {
                throw new Error("Invalid image resize worker request");
            }
            const result = await resizeImageInProcess(message.inputBytes, message.mimeType, message.options);
            const response = { type: IMAGE_RESIZE_WORKER_RESPONSE_TYPE, result };
            port.postMessage(response);
        }
        catch (error) {
            const response = {
                type: IMAGE_RESIZE_WORKER_RESPONSE_TYPE,
                error: error instanceof Error ? error.message : String(error),
            };
            port.postMessage(response);
        }
    })();
});
//# sourceMappingURL=image-resize-worker.js.map