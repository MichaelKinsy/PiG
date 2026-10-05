// Reproduces the shape of the Pi agent-state reporter that herdr installs
// (herdr-agent-state.ts, HERDR_INTEGRATION_ID=pi): env-gated factory, TUI-only
// session_start that awaits a socket report, agent_start/agent_settled gated on
// ctx.isIdle(), and a single-flight queue that drains the latest state over a
// short-lived unix-socket connection with an unref'd timeout.
import net from "node:net";
import path from "node:path";

const socketPath = process.env.HERDR_SOCKET_PATH;
const paneId = process.env.HERDR_PANE_ID;
const source = "herdr:pi";

function enabled() {
  return process.env.HERDR_ENV === "1" && !!socketPath && !!paneId;
}

function sendRequestAttempt(request, timeoutMs) {
  return new Promise((resolve) => {
    let done = false;
    let timeout;
    const finish = (delivered) => {
      if (done) return;
      done = true;
      if (timeout) clearTimeout(timeout);
      socket.destroy();
      resolve(delivered);
    };
    const socket = net.createConnection(socketPath);
    socket.on("error", () => finish(false));
    socket.on("connect", () => socket.write(`${JSON.stringify(request)}\n`));
    socket.on("data", () => finish(true));
    socket.on("end", () => finish(false));
    timeout = setTimeout(() => finish(false), timeoutMs);
    timeout.unref?.();
  });
}

async function sendRequest(request) {
  if (await sendRequestAttempt(request, 500)) return;
  await sendRequestAttempt(request, 1500);
}

let seq = 0;
let sessionPath;
let sessionId;
let inFlight = false;
let queued;

function withSession(params) {
  if (sessionPath) return { ...params, agent_session_path: sessionPath };
  if (sessionId) return { ...params, agent_session_id: sessionId };
  return params;
}

function queueState(state) {
  queued = { state, seq: ++seq };
  if (!inFlight) void drain();
}

async function drain() {
  if (inFlight) return;
  inFlight = true;
  try {
    while (queued) {
      const next = queued;
      queued = undefined;
      await sendRequest({
        id: `${source}:${next.seq}`,
        method: "pane.report_agent",
        params: withSession({ pane_id: paneId, source, agent: "pi", state: next.state, seq: next.seq }),
      });
    }
  } finally {
    inFlight = false;
    if (queued) void drain();
  }
}

function updateSessionRef(ctx) {
  const file = ctx?.sessionManager?.getSessionFile?.();
  sessionPath = typeof file === "string" && (path.posix.isAbsolute(file) || path.win32.isAbsolute(file)) ? file : undefined;
  const id = ctx?.sessionManager?.getSessionId?.();
  sessionId = typeof id === "string" && id.length > 0 ? id : undefined;
}

export default function (pi) {
  if (!enabled()) return;

  let root = false;
  let active = false;
  let last;
  const publish = (force = false) => {
    const next = active ? "working" : "idle";
    if (!force && next === last) return;
    last = next;
    queueState(next);
  };

  pi.on("session_start", async (event, ctx) => {
    if (ctx?.mode !== "tui") return;
    root = true;
    updateSessionRef(ctx);
    await sendRequest({
      id: `${source}:session:${++seq}`,
      method: "pane.report_agent_session",
      params: { pane_id: paneId, source, agent: "pi", seq, session_start_source: event?.reason, agent_session_path: sessionPath },
    });
    active = ctx?.isIdle?.() === false;
    publish(true);
  });

  pi.on("agent_start", (_event, ctx) => {
    if (!root) return;
    updateSessionRef(ctx);
    active = true;
    publish();
  });

  pi.on("agent_settled", (_event, ctx) => {
    if (!root || ctx?.isIdle?.() !== true) return;
    active = false;
    publish();
  });
}
