// Run the URL expressions of Pi's radius.ts and radius-config.ts over probes from stdin (the expressions are copied: they are inline in async functions).
// usage: node radius_urls.mjs <path to radius-config.ts>
// {op: "resolve", gateway, path}: new URL(path, normalizeRadiusGatewayUrl(gateway)).toString() (loadRadiusGatewayConfig, loadRadiusOAuthDiscovery, requestOAuthToken, requestDeviceAuthorization)
// {op: "resolveRaw", gateway, path}: new URL(path, gateway).toString() with the gateway as given (loadRadiusGatewayConfig does not normalize)
// {op: "authorize", endpoint, query}: const u = new URL(endpoint); u.search = query; u.toString() (loginWithBrowser)
// Result: the string, or null when the URL constructor throws.
import { pathToFileURL } from "node:url";
const { normalizeRadiusGatewayUrl } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			try {
				if (p.op === "resolve") return new URL(p.path, normalizeRadiusGatewayUrl(p.gateway)).toString();
				if (p.op === "resolveRaw") return new URL(p.path, p.gateway).toString();
				const url = new URL(p.endpoint);
				url.search = p.query;
				return url.toString();
			} catch (error) {
				if (!(error instanceof TypeError)) throw error;
				return null;
			}
		}),
	),
);
