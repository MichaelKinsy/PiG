// Run requireSecureTokenEndpoint, cut from the vendored @anthropic-ai/sdk 0.129.0 lib/credentials/types.mjs (WorkloadIdentityError is stubbed with the message), over base URLs from stdin.
// usage: node anthropic_token_endpoint.mjs <path to types.mjs>
// Result: null when the endpoint is accepted, otherwise the error message.
import { readFileSync } from "node:fs";
const source = readFileSync(process.argv[2], "utf8");
const cut = /^export function requireSecureTokenEndpoint[\s\S]*?\n}\n/m.exec(source);
if (!cut) throw new Error("requireSecureTokenEndpoint not found");
class WorkloadIdentityError extends Error {}
const requireSecureTokenEndpoint = new Function("WorkloadIdentityError", `${cut[0].replace("export function", "function")}\nreturn requireSecureTokenEndpoint;`)(WorkloadIdentityError);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((baseUrl) => {
			try {
				requireSecureTokenEndpoint(baseUrl);
				return null;
			} catch (error) {
				if (!(error instanceof WorkloadIdentityError)) throw error;
				return error.message;
			}
		}),
	),
);
