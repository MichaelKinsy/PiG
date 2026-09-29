import { writeFileSync } from "node:fs";
import { join } from "node:path";

export default function (pi) {
  pi.on("before_agent_start", async (event) => {
    const dimensions = (event.images ?? []).map((image) => {
      const png = Buffer.from(image.data, "base64");
      return [png.readUInt32BE(16), png.readUInt32BE(20)];
    });
    writeFileSync(join(process.env.PARITY_IMAGE_DIR, "images.json"), JSON.stringify({ dimensions, omitted: event.prompt.includes("Image omitted") }) + "\n");
  });
}
