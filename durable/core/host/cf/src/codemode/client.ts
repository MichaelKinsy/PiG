// The client half of Pi 1.1.0+ea448f45 packages/codemode/src/runtime/remote.ts (remoteLauncher, RemoteCodemodeSandbox and its options), ported
// verbatim apart from the import paths: the serving half is serve.ts. The remote it reaches is a CodemodeSandboxDurableObject (sandbox-object.ts),
// a stub of one over Workers RPC, or any object with `execute`.
import { CodemodeSandboxBase, errorMessage, type VmLauncher } from "./execution.ts";
import {
	type CodemodeRemote,
	type CodemodeRemoteExchange,
	type CodemodeRemoteReply,
	type CodemodeRemoteRequest,
	isWorkerToHostMessage,
} from "./protocol.ts";
import type { CodemodeSandboxBaseOptions } from "./types.ts";

export interface RemoteCodemodeSandboxOptions extends CodemodeSandboxBaseOptions {
	/**
	 * Returns the remote that runs one execution, called once per `execute()`. For a Durable Object,
	 * pick the instance here, for example `() => env.CODEMODE_SANDBOX.getByName(sessionId)`.
	 */
	remote: () => CodemodeRemote;
	/** Interrupt budget requested from the remote. Default: the remote's default. */
	interruptBudget?: number;
}


export function remoteLauncher(remote: () => CodemodeRemote, interruptBudget: number | undefined): VmLauncher {
	return (data, events) => {
		let stopped = false;
		let settled = false;
		const waiting = new Map<number, (reply: CodemodeRemoteReply) => void>();

		const exchange: CodemodeRemoteExchange = (messages) =>
			new Promise<CodemodeRemoteReply>((resolve) => {
				if (stopped || !Array.isArray(messages)) {
					resolve({ type: "stop" });
					return;
				}
				// Register the waiter first: the host may answer a call synchronously, for example when the
				// tool throws before returning a promise.
				const call = messages.find((message) => isWorkerToHostMessage(message) && message.type === "call");
				if (call?.type === "call") waiting.set(call.id, resolve);
				else resolve(null);
				for (const message of messages) {
					if (typeof message === "object" && message !== null && message.type === "failure") {
						settled = true;
						events.failure(message.error);
						continue;
					}
					// Malformed messages still go to the host, which fails the execution as a broken bridge.
					events.message(message);
					if (isWorkerToHostMessage(message) && (message.type === "done" || message.type === "crash"))
						settled = true;
				}
			});

		let target: CodemodeRemote;
		try {
			target = remote();
		} catch (error) {
			throw new Error(`Failed to reach the remote sandbox: ${errorMessage(error)}`);
		}
		const request: CodemodeRemoteRequest = interruptBudget === undefined ? { data } : { data, interruptBudget };
		Promise.resolve()
			.then(() => target.execute(request, exchange))
			.then(
				() => {
					if (!settled && !stopped) {
						events.failure({ kind: "sandbox", message: "Remote sandbox returned before the script settled" });
					}
				},
				(error: unknown) => {
					if (!stopped)
						events.failure({ kind: "sandbox", message: `Remote sandbox failed: ${errorMessage(error)}` });
				},
			);

		return {
			post: (message) => {
				const resolve = waiting.get(message.id);
				if (!resolve) return;
				waiting.delete(message.id);
				resolve(message);
			},
			stop: async () => {
				stopped = true;
				for (const resolve of waiting.values()) resolve({ type: "stop" });
				waiting.clear();
			},
		};
	};
}

/**
 * Runs each execution's VM on a remote, usually a Cloudflare Durable Object deployed with
 * `CodemodeSandboxDurableObject`, while tools run here. Same API and results as
 * `CodemodeSandbox`, so it can replace it. Differences: a script that computes without calling a
 * tool is limited by the remote's interrupt budget rather than `timeoutMs`, and after a deadline or
 * abort the remote VM keeps computing until it next calls a tool or uses up that budget.
 */
export class RemoteCodemodeSandbox extends CodemodeSandboxBase {
	constructor(options: RemoteCodemodeSandboxOptions) {
		super(options, remoteLauncher(options.remote, options.interruptBudget));
	}
}
