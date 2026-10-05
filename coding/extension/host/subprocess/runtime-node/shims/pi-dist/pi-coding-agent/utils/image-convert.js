import { getCapabilities, setImageTranscoder } from "../../../pi-tui.mjs";
import { applyExifOrientation } from "./exif-orientation.js";
import { loadPhoton } from "./photon.js";
function encodePng(photon, bytes) {
    try {
        const rawImage = photon.PhotonImage.new_from_byteslice(bytes);
        const image = applyExifOrientation(photon, rawImage, bytes);
        if (image !== rawImage)
            rawImage.free();
        try {
            return new Uint8Array(image.get_bytes());
        }
        finally {
            image.free();
        }
    }
    catch {
        // Conversion failed
        return null;
    }
}
export async function convertImageBytesToPng(bytes) {
    const photon = await loadPhoton();
    if (!photon) {
        // Photon not available, can't convert
        return null;
    }
    return encodePng(photon, bytes);
}
/**
 * Convert image to PNG format for terminal display.
 * Kitty graphics protocol requires PNG format (f=100).
 */
export async function convertToPng(base64Data, mimeType) {
    // Already PNG, no conversion needed
    if (mimeType === "image/png") {
        return { data: base64Data, mimeType };
    }
    const bytes = new Uint8Array(Buffer.from(base64Data, "base64"));
    const pngBytes = await convertImageBytesToPng(bytes);
    if (!pngBytes) {
        return null;
    }
    return {
        data: Buffer.from(pngBytes).toString("base64"),
        mimeType: "image/png",
    };
}
/**
 * Load photon and return a synchronous PNG transcoder for pi-tui's Kitty image rendering.
 * Returns undefined if photon cannot be loaded.
 */
export async function loadPngTranscoder() {
    const photon = await loadPhoton();
    if (!photon)
        return undefined;
    return (base64Data) => {
        const pngBytes = encodePng(photon, new Uint8Array(Buffer.from(base64Data, "base64")));
        return pngBytes ? Buffer.from(pngBytes).toString("base64") : null;
    };
}
let pngTranscoderLoad;
let pngTranscoderRegistered = false;
/**
 * On Kitty-protocol terminals, register photon as pi-tui's image transcoder so non-PNG images render.
 * Loads photon once. `onRegistered` runs after registration so callers can re-render images that
 * showed text fallbacks; it is not called if the transcoder was already registered or cannot load.
 */
export function ensurePngTranscoder(onRegistered) {
    if (pngTranscoderRegistered || getCapabilities().images !== "kitty")
        return;
    pngTranscoderLoad ??= loadPngTranscoder().then((transcoder) => {
        if (!transcoder)
            return false;
        setImageTranscoder(transcoder);
        pngTranscoderRegistered = true;
        return true;
    });
    void pngTranscoderLoad.then((registered) => {
        if (registered)
            onRegistered();
    });
}
//# sourceMappingURL=image-convert.js.map