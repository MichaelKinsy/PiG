// PiG's OAuth callback pages over the corpus of ai/oauth_page_corpus_test.go; the owning Go test emits one JSON line per page.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './ai', '-run', '^TestOAuthPageCorpusParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`oauth page probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^oauth-page-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('oauth page probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
