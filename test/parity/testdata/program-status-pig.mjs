// PiG's ProgramStatusReporter over the corpus of internal/codingagent/program_status_reporter_oracle_test.go; the owning Go test emits one JSON line of reports per sequence.
import { spawnSync } from 'node:child_process';
const result = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./internal/codingagent', '-run', '^TestProgramStatusReporterParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`program status probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^program-status-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('program status probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
