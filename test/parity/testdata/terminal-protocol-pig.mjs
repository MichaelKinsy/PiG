// Compare the real ProcessTerminal negotiation after its keyboard protocol query, as terminal-protocol-pi.mjs does.
// The owning Go test exposes observations because queryAndEnableKittyProtocol is intentionally private, not a public test-only API.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './tui', '-run', '^TestTerminalProtocolParityObservations$', '-v', '-count=1'], { encoding: 'utf8' });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`terminal protocol probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /terminal-protocol-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length !== 1) throw new Error(`terminal protocol probe emitted ${observations.length} observations, want 1`);
process.stdout.write(observations[0] + '\n');
