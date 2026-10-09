// Runs the pinned packages/client discoverUnixServers over a directory and prints the routes as JSON.
import { discoverUnixServers } from '@earendil-works/pi-client/unix';

const [directory, timeoutMs] = process.argv.slice(2);
const routes = await discoverUnixServers({ directory, timeoutMs: timeoutMs === undefined ? undefined : Number(timeoutMs) });
process.stdout.write(`${JSON.stringify(routes)}\n`);
