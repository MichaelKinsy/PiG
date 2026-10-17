// PiG's llama.cpp classifier prompt (renderPrompt) over the probes of ai/llama_cpp_render_prompt_oracle_test.go; the owning Go test emits one JSON line per probe.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './ai', '-run', '^TestLlamaCppRenderPromptParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`llama prompt probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^llama-prompt-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('llama prompt probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
