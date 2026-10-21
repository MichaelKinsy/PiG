// PiG's getSystemMessageText and renderSystemMessageUpdate over the corpus of ai/system_message_text_oracle_test.go; the owning Go test emits one line per message.
import { spawnSync } from 'node:child_process';
const result = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./ai', '-run', '^TestSystemMessageDocsParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`system message probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^system-message-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('system message probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
