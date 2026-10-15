// Generates ssh_arguments_cases.json from Pi's sshArguments (packages/env/src/ssh.ts, which imports only Node's
// built-ins and ./connection.ts, so Node 24 runs it directly). Run from the repository root:
//   node env/testdata/generate_ssh_arguments.mjs > env/testdata/ssh_arguments_cases.json
// The source tree is .upstream/current unless PI_UPSTREAM_ROOT names another.
import { pathToFileURL } from "node:url";
import path from "node:path";

const root = process.env.PI_UPSTREAM_ROOT ?? ".upstream/current";
const { sshArguments } = await import(pathToFileURL(path.resolve(root, "packages/env/src/ssh.ts")).href);

const base = { host: "gpu-box", knownHostsFile: "/data/ssh/known_hosts", hostKeyAlias: "pi-env-gpu" };
const targets = [
	base,
	{ ...base, user: "me", port: 2222, identityFile: "/keys/id_ed25519", configFile: "/etc/ssh/pi_config" },
	{ ...base, knownHostsFile: "/data/100%/known_hosts", identityFile: "/k/%d/id" },
	{ ...base, port: 1 },
	{ ...base, port: 65535 },
	{ ...base, host: "-oProxyCommand=evil" },
	{ ...base, host: "" },
	{ ...base, host: "a b" },
	{ ...base, host: "a\u0001b" },
	{ ...base, host: "a\u007fb" },
	{ ...base, hostKeyAlias: "-x" },
	{ ...base, hostKeyAlias: "a\tb" },
	// JavaScript's \s is wider than ASCII whitespace.
	{ ...base, host: "a\u00a0b" },
	{ ...base, host: "a\u2028b" },
	{ ...base, hostKeyAlias: "a\u3000b" },
	{ ...base, hostKeyAlias: "a\ufeffb" },
	{ ...base, user: "me\u2009" },
	{ ...base, host: "a\u0085b" },
	{ ...base, host: "h\u00e9te" },
	{ ...base, user: "-l" },
	{ ...base, user: "me you" },
	{ ...base, port: 0 },
	{ ...base, port: 65536 },
	{ ...base, port: 22.5 },
	{ ...base, port: -1 },
	{ ...base, knownHostsFile: "" },
	{ ...base, knownHostsFile: 'a"b' },
	{ ...base, knownHostsFile: "a\nb" },
	{ ...base, identityFile: "" },
	{ ...base, user: "" },
	{ ...base, configFile: "" },
	{ ...base, configFile: "-x y" },
	{ ...base, identityFile: 'k"x' },
];
const cases = [];
for (const target of targets) {
	for (const strict of [true, false]) {
		const entry = { target, strictHostKeys: strict, knownHostsFile: strict ? null : "/tmp/scan/known_hosts" };
		try {
			entry.args = strict ? sshArguments(target) : sshArguments(target, false, "/tmp/scan/known_hosts");
		} catch (error) {
			entry.error = error.message;
		}
		cases.push(entry);
	}
}
process.stdout.write(`${JSON.stringify({ cases })}\n`);
