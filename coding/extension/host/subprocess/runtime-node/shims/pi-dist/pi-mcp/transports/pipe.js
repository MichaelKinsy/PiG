import { McpConnectionClosedError } from "../protocol/jsonrpc.js";
import { JsonRpcLineReader } from "./lines.js";
import { TransportEvents } from "./transport.js";
const DEFAULT_CLOSE_TIMEOUT_MS = 2_000;
/**
 * MCP's stdio transport over a program that something else runs, for example on another machine through pi-env.
 * The transport closes when the program ends; a rejected `exited` that `close()` did not cause is reported as an
 * error first.
 */
export class PipeTransport extends TransportEvents {
    options;
    lines;
    pipe;
    started = false;
    closed = false;
    ended = false;
    constructor(options) {
        super();
        this.options = Object.freeze({ ...options });
        this.lines = new JsonRpcLineReader({
            maxMessageBytes: this.options.maxMessageBytes,
            maxStderrBytes: this.options.maxStderrBytes,
            onStderr: this.options.onStderr,
            onMessage: (message) => this.emitMessage(message),
            onError: (error) => this.emitError(error),
        });
    }
    /** The last `maxStderrBytes` (default 64 KiB) of the program's stderr. */
    get stderr() {
        return this.lines.stderr;
    }
    async start() {
        if (this.started)
            throw new Error("MCP pipe transport already started");
        if (this.closed)
            throw new McpConnectionClosedError();
        this.started = true;
        const pipe = await this.options.open({
            onStdout: (bytes) => this.lines.readStdout(bytes),
            onStderr: (bytes) => this.lines.readStderr(bytes),
        });
        if (this.closed) {
            // close() ran while the program was starting and found nothing to shut down. Nobody else awaits
            // `exited`, and a pipe may reject it after a kill.
            pipe.exited.catch(() => { });
            pipe.close();
            throw new McpConnectionClosedError();
        }
        this.pipe = pipe;
        pipe.exited.then(() => this.exit(), (error) => this.fail(error));
    }
    async send(message) {
        if (!this.pipe || this.closed || this.ended)
            throw new McpConnectionClosedError();
        this.pipe.write(`${JSON.stringify(message)}\n`);
    }
    async close() {
        if (this.closed)
            return;
        this.closed = true;
        const pipe = this.pipe;
        if (!pipe) {
            this.emitClose();
            return;
        }
        if (this.ended)
            return;
        // Shutdown per the spec: close stdin and let the program exit, then kill it.
        pipe.closeStdin();
        let timer;
        const exitedInTime = await Promise.race([
            pipe.exited.then(() => true, () => true),
            new Promise((resolve) => {
                timer = setTimeout(() => resolve(false), this.options.closeTimeoutMs ?? DEFAULT_CLOSE_TIMEOUT_MS);
            }),
        ]);
        clearTimeout(timer);
        if (exitedInTime)
            return;
        pipe.close();
        await pipe.exited.catch(() => { });
    }
    /** The program ended: report an unfinished message, then close. */
    exit() {
        this.ended = true;
        this.lines.end();
        this.emitClose();
    }
    /** `exited` rejected: an error unless `close()` caused it, then the same as an exit. */
    fail(error) {
        if (!this.closed)
            this.emitError(error);
        this.exit();
    }
}
//# sourceMappingURL=pipe.js.map