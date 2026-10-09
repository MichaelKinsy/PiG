import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '1.1.0');
const { SettingsManager } = await import(pathToFileURL(join(root, 'dist/core/settings-manager.js')));
const { DefaultResourceLoader } = await import(pathToFileURL(join(root, 'dist/core/resource-loader.js')));
const dir = mkdtempSync(join(tmpdir(), 'pi-settings-memory-'));
function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])]));
  return value;
}
function print(settings) {
  console.log('SETTINGS_MEMORY ' + JSON.stringify(canonical([settings.getDefaultThinkingLevel() ?? '', settings.getTheme() ?? '', settings.getImageAutoResize(), settings.getCompactionEnabled(), settings.getGlobalSettings()])));
}
try {
  const initial = { defaultThinkingLevel: 'high', images: { autoResize: false }, compaction: { enabled: false } };
  const direct = SettingsManager.inMemory(initial);
  await direct.reload(); print(direct);
  const settings = SettingsManager.inMemory(initial);
  const loader = new DefaultResourceLoader({ cwd: dir, agentDir: join(dir, 'agent'), settingsManager: settings, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true });
  await loader.reload(); print(settings);
  const changed = SettingsManager.inMemory({ images: { autoResize: false }, compaction: { enabled: false } });
  changed.setTheme('dark'); await changed.flush(); await changed.reload(); print(changed);
} finally { rmSync(dir, { recursive: true, force: true }); }
