// The worker's HostData (Pi's src/node/app/data.ts `hostDataFacet`), served on the worker's NodeHost, the host that serves the worker's
// address. It is the server's only: any other principal is refused when it gets the object. `exportTo` is forwarded to the Go kit's
// local `HostDataExport` (data.go) with this start's token. The kit writes the module at the worker's start with the server's
// principal and the token in place of the two markers.
import { ChordError, defineFacet, local, method, obj, remote, str } from "@earendil-works/chord";

const SERVER = __SERVER__;
const TOKEN = __TOKEN__;

const canonical = (value) =>
	Array.isArray(value)
		? `[${value.map(canonical).join(",")}]`
		: value !== null && typeof value === "object"
			? `{${Object.keys(value)
					.sort()
					.map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`)
					.join(",")}}`
			: JSON.stringify(value);
const SERVER_KEY = canonical(SERVER);

const HostData = remote("chord.app.data", {
	version: 1,
	params: { address: str() },
	hostAddress: ({ address }) => JSON.parse(address),
	variants: { main: { methods: { exportTo: method(obj({ path: str(), manifest: str() })) } } },
});

const HostDataExport = local("chord.app.data.export", {
	version: 1,
	params: {},
	members: { exportTo: method(obj({ token: str(), path: str(), manifest: str() })) },
});

export default defineFacet((host) =>
	host.serve(HostData, (_params, principal, self) => {
		if (principal === undefined || canonical(principal) !== SERVER_KEY) {
			throw new ChordError({ code: "forbidden", message: "chord.app.data: the server's only" });
		}
		const exporter = self.get(HostDataExport, {});
		return {
			variant: "main",
			members: { exportTo: ({ path, manifest }, ctx) => exporter.exportTo({ token: TOKEN, path, manifest }, ctx) },
		};
	}),
);
