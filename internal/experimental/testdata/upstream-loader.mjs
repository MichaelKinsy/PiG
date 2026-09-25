import { registerHooks, createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';

// Resolve installed dependencies without modifying the read-only upstream mirror.
const require = createRequire(pathToFileURL(`${process.env.PIG_TEST_ROOT}/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/package.json`));
registerHooks({
  resolve(specifier, context, nextResolve) {
    try { return nextResolve(specifier, context); }
    catch (error) {
      if (error.code !== 'ERR_MODULE_NOT_FOUND' || specifier.startsWith('.') || specifier.startsWith('/') || specifier.includes(':')) throw error;
      return nextResolve(pathToFileURL(require.resolve(specifier)).href, context);
    }
  },
});
