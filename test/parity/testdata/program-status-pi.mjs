// Pi 1.1.0 ProgramStatusReporter over the corpus the Go test TestProgramStatusReporterProbeDump prints: one JSON line of reports per sequence.
import { PI_PACKAGE_VERSION } from "./pi-version.mjs";
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './internal/codingagent', '-run', '^TestProgramStatusReporterProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('program-status-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`program status probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['internal/codingagent/testdata/program_status_reporter.mjs', PI_PACKAGE_VERSION], { input: line.slice('program-status-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`program status oracle exited ${result.status}`);
}
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
for (const reports of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(reports).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029') + '\n');
