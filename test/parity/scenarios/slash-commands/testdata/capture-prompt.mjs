import { writeFileSync } from "node:fs";

export default function (pi) {
  pi.on("before_agent_start", (event) => {
    const [destination, ...prompt] = event.prompt.split("\n");
    writeFileSync(destination, JSON.stringify(prompt.join("\n")) + "\n");
  });
}
