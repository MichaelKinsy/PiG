// PiG's llama.cpp classifier question rendering over the corpus of ai/llama_cpp_render_oracle_test.go; the owning Go test emits one JSON line per request.
import { spawnSync } from 'node:child_process';
const result = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./ai', '-run', '^TestLlamaCppRenderDocsParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`llama render probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^llama-render-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('llama render probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
