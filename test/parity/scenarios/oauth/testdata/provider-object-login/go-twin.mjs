// Pi's side of wiring-go/extension.go: the same provider object in TypeScript.
import { registerWiringProvider } from "./provider.mjs";

export default (pi) => registerWiringProvider(pi, "wiring-go", "Wiring Go");
