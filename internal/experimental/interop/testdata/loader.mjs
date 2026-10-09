// Resolves the pinned Pi 1.0.4 workspace packages from their TypeScript sources so the oracle runs the exact upstream code,
// and every external dependency from the installed SDK tree (the upstream mirror is read-only and carries no node_modules).
import { createRequire, registerHooks } from 'node:module';
import { pathToFileURL } from 'node:url';

const root = process.env.PIG_TEST_ROOT;
const packages = `${root}/.upstream/current/packages`;
const workspace = {
  '@earendil-works/pi-protocol': `${packages}/protocol/src/index.ts`,
  '@earendil-works/pi-client': `${packages}/client/src/index.ts`,
  '@earendil-works/pi-client/unix': `${packages}/client/src/unix.ts`,
  '@earendil-works/pi-server': `${packages}/server/src/index.ts`,
  '@earendil-works/pi-server/unix': `${packages}/server/src/transports/unix/index.ts`,
  '@earendil-works/pi-server/testing': `${packages}/server/src/testing/index.ts`,
  '@earendil-works/chord': `${packages}/chord/src/index.ts`,
  '@earendil-works/chord/context': `${packages}/chord/src/context/index.ts`,
  '@earendil-works/chord/delta': `${packages}/chord/src/delta/index.ts`,
  '@earendil-works/chord/node': `${packages}/chord/src/node.ts`,
};
const installed = createRequire(pathToFileURL(`${root}/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/package.json`));
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (workspace[specifier]) return nextResolve(pathToFileURL(workspace[specifier]).href, context);
    try { return nextResolve(specifier, context); }
    catch (error) {
      if (error.code !== 'ERR_MODULE_NOT_FOUND' || specifier.startsWith('.') || specifier.startsWith('/') || specifier.includes(':')) throw error;
      return nextResolve(pathToFileURL(installed.resolve(specifier)).href, context);
    }
  },
});
