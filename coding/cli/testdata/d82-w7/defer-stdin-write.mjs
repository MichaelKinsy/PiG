// Preloaded into rpc-usage-probe.mjs (node --import) by TestRPCUsageProbeFailsWhenTheCLIWritesNoFrame: child.stdin.write is held until the child has written to its stdout, by which point the test's CLI has closed its end of stdin. The write then fails with EPIPE every run, instead of only when the host is slow enough for a CLI to close stdin before the probe writes.
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';

const spawn = childProcess.spawn;
childProcess.spawn = (...args) => {
	const child = spawn(...args);
	const write = child.stdin.write.bind(child.stdin);
	child.stdin.write = (...writeArgs) => {
		child.stdout.once('data', () => write(...writeArgs));
		return true;
	};
	return child;
};
syncBuiltinESMExports();
