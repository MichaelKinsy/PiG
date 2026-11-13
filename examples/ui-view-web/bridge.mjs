#!/usr/bin/env node
// A bridge between a browser and pig, for the plugin UI carrier (docs/ui-view-carrier.md). A browser cannot speak lines
// on a pipe or on a Unix socket, so this program serves the page, relays the lines pig writes as Server-Sent Events and
// the lines the page POSTs to pig. It uses Node's standard library only and is not part of pig: pig gains no web server.
//
//   node bridge.mjs [--port <n>] [--pig <path>] [-- <pig arguments>]
//       starts `pig --mode rpc` as a child for every page that connects: every page has a session of its own.
//   node bridge.mjs [--port <n>] --connect <socket, or the directory it is in> [--wait <seconds>]
//       starts no pig: every page is one more client of the view endpoint of a running interactive pig, beside its
//       terminal (Piglet keys `viewClient: terminal` and `viewEndpoint: socket`).
//   --theme <directory>, with either form
//       gives the page a presentation of the caller's: the directory's theme.css is linked from the page, and the
//       files theme.css names (images, fonts) are served from the same directory under /theme/. Without it the page
//       is index.html as it is.
//
// It listens on 127.0.0.1 only and prints the page's address, which carries a token every request must repeat.
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { readdirSync, statSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { createConnection } from "node:net";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// A command line of the carrier is at most 4 MiB; the bridge reads no more than that from a page.
const MAX_LINE_BYTES = 4 << 20;
// The seven commands of the carrier. With --connect a page's line is passed on only when its type is one of them.
const CARRIER_COMMANDS = new Set(["ui_view_attach", "ui_view_abilities", "ui_view_resync", "ui_view_detach", "ui_view_intent", "ui_view_intent_cancel", "ui_view_report"]);
// With --connect, the bytes one side may be owed before the bridge holds no more for it. It is what a view endpoint
// itself lets a connection owe (maxOutBytes in internal/viewendpoint: two of the carrier's event queues). A page that is
// owed this much has stopped reading and is cut; a line for a session that is owed this much is refused.
const QUEUE_BYTES = 8 << 20;
// With --connect, the pause before the next try for a connection. A try is one stat, one directory listing and one
// connect for each socket there, so ten a second for a waiting page cost nothing that shows, and the page is attached
// again within 100 ms of the next session's socket, which a person does not see as a delay.
const RETRY_MS = 100;

const argv = process.argv.slice(2);
const rest = argv.includes("--") ? argv.splice(argv.indexOf("--")).slice(1) : [];
const option = (name, fallback) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : fallback);
const port = Number(option("--port", "0"));
const pig = option("--pig", process.env.PIG_BIN || "pig");
// The socket of a running session or the directory that holds it; "" starts a pig for every page instead.
const target = argv.includes("--connect") ? resolve(option("--connect") ?? "") : "";
// With --connect, how long a page waits for a connection the session uses before it is told to give up.
const waitMs = Number(option("--wait", "60")) * 1000;
if (argv.includes("--connect") && (option("--connect") === undefined || rest.length > 0 || !(waitMs >= 0))) {
  console.error("usage: node bridge.mjs --connect <socket, or the directory it is in> [--wait <seconds>] [--port <n>]\n--connect starts no pig, so nothing follows `--`");
  process.exit(2);
}
// A theme is a directory with a theme.css. Its files are served by name, with the type of their extension; a name
// with a separator, a leading dot or another extension is not found, so nothing outside the directory is read.
const theme = argv.includes("--theme") ? resolve(option("--theme") ?? "") : "";
const THEME_TYPES = { css: "text/css; charset=utf-8", svg: "image/svg+xml", png: "image/png", woff2: "font/woff2", json: "application/json" };
const THEME_LINK = '<link rel="stylesheet" href="theme/theme.css">\n';
if (argv.includes("--theme") && (option("--theme") === undefined || !statSync(join(theme, "theme.css"), { throwIfNoEntry: false })?.isFile())) {
  console.error("usage: node bridge.mjs --theme <directory> ...\nthe directory must hold a theme.css");
  process.exit(2);
}
const token = randomBytes(16).toString("hex");
const files = { "/": ["index.html", "text/html; charset=utf-8"], "/client.js": ["client.js", "text/javascript; charset=utf-8"] };
// Where the lines of a page go, by the session it was told: deliver(line) passes one line on or returns the refusal
// [status, text], and close() ends the session.
const sessions = new Map();
// With --connect, the pages with an open stream: each holds its connection, or the tries for one, and at most one timer.
const pages = new Set();

function refuse(res, status, text) {
  res.writeHead(status, { "content-type": "text/plain; charset=utf-8" });
  res.end(text + "\n");
}

// A request is the page's own: it names this server as its host and, when the browser states an origin, this origin.
function own(req) {
  const hosts = [`127.0.0.1:${server.address().port}`, `localhost:${server.address().port}`];
  if (!hosts.includes(req.headers.host)) return false;
  return req.headers.origin === undefined || hosts.some((host) => req.headers.origin === `http://${host}`);
}

// events starts one pig for the page and relays its lines. The relay keeps pig's order, and when the page reads slower
// than pig writes it stops reading pig's stdout, so pig sees a client that stopped reading and the carrier's own rule
// applies: a bounded queue, then one ui_view_gap.
function events(req, res) {
  const id = randomBytes(8).toString("hex");
  const child = spawn(pig, ["--mode", "rpc", ...rest], { stdio: ["pipe", "pipe", "inherit"] });
  sessions.set(id, { deliver: (line) => void child.stdin.write(line + "\n"), close: () => child.kill() });
  res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-store", connection: "keep-alive" });
  res.write(`event: session\ndata: ${id}\n\n`);
  let partial = "";
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    const lines = (partial + chunk).split("\n");
    partial = lines.pop();
    let ready = true;
    for (const line of lines) ready = res.write(`data: ${line}\n\n`);
    if (!ready) {
      child.stdout.pause();
      res.once("drain", () => child.stdout.resume());
    }
  });
  child.on("error", (err) => res.end(`event: end\ndata: ${JSON.stringify({ error: String(err) })}\n\n`));
  child.on("exit", (code) => {
    sessions.delete(id);
    res.end(`event: end\ndata: ${JSON.stringify({ code })}\n\n`);
  });
  child.stdin.on("error", () => {});
  // The page went away: closing pig's stdin ends the session, which ends its attachment.
  res.on("close", () => {
    sessions.delete(id);
    child.stdin.end();
    setTimeout(() => child.kill(), 5000).unref();
  });
}

// locate names the sockets a page may be connected to: the target when it is a socket, every socket in it when it is
// a directory. It returns { paths, none: why there is no socket } or { refuse: why there never will be one }.
function locate() {
  try {
    if (statSync(target).isSocket()) return { paths: [target] };
    const paths = readdirSync(target).filter((name) => name.endsWith(".sock")).sort().map((name) => join(target, name));
    return { paths, none: `no running session: no socket in ${target}` };
  } catch (err) {
    if (err.code === "ENOENT") return { paths: [], none: `no running session: ${target} does not exist` };
    return { refuse: err.code === "ENOTDIR" ? `${target} is neither a socket nor a directory` : err.message };
  }
}

// forward writes a page's line to the session's socket when it is a command of the carrier, and refuses it otherwise:
// a view endpoint serves the carrier only, so the bridge passes on nothing else. The line goes on as the page wrote it.
function forward(socket, line) {
  let command;
  try {
    command = JSON.parse(line);
  } catch {
    return [400, "the line is not JSON"];
  }
  if (command === null || typeof command !== "object" || Array.isArray(command)) return [400, "the line is not a JSON object"];
  // pig reads member names without regard to case, and the last of several wins: a line with a second member that
  // spells type differently could name one command here and another there.
  const types = Object.keys(command).filter((name) => name.toLowerCase() === "type");
  if (types.length !== 1 || types[0] !== "type" || !CARRIER_COMMANDS.has(command.type)) {
    return [403, `a session's view endpoint takes the seven ui_view_* commands only: "${String(command.type).slice(0, 64)}" is not passed on`];
  }
  if (socket.writableLength >= QUEUE_BYTES) return [503, "the session has not read what this page sent it"];
  socket.write(line + "\n");
}

const DATA = Buffer.from("data: ");
const BLANK = Buffer.from("\n\n");

// follow makes one page a client of the session's view endpoint. It holds one connection for the page and relays the
// endpoint's lines in their order. A reload keeps the registry and so the connection; the mounts of the extensions it reloads arrive broken. The
// session closes the connection when its registry ends (a new, resumed, forked or switched session, the exit): the
// page is then sent `end`, the bridge tries again every RETRY_MS while no session takes a connection, and with the
// next connection the page is sent `session` again, so it attaches to the new registry and draws what that one holds.
// A page is told why it waits, and after --wait seconds without a connection the session used, its stream ends.
function follow(req, res) {
  res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-store", connection: "keep-alive" });
  const page = { sockets: new Set(), timer: undefined };
  pages.add(page);
  let gone = false; // nothing more is written to the page
  let told = ""; // why the page was last told it waits
  let deadline = Date.now() + waitMs;
  const event = (name, data) => res.write(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`);
  // leave ends what the bridge holds for the page: its timer and its connections.
  const leave = () => {
    gone = true;
    pages.delete(page);
    clearTimeout(page.timer);
    for (const socket of page.sockets) socket.destroy();
  };
  const stop = (reason) => {
    event("end", { retry: false, reason });
    res.end();
    leave();
  };
  const wait = (reason) => {
    if (Date.now() >= deadline) return stop(`${reason}. The bridge waited ${waitMs / 1000} s; reload the page to try again.`);
    if (reason !== told) event("endpoint", { state: "waiting", reason });
    told = reason;
    page.timer = setTimeout(connect, RETRY_MS);
  };
  // connect tries every socket there is. One that takes no connection belongs to a pig that ended without removing
  // it: it counts for nothing and is left where it is. Exactly one that takes a connection is the session, and that
  // connection is the page's. Among several the bridge does not choose.
  const connect = () => {
    page.timer = undefined;
    const found = locate();
    if (found.refuse) return stop(found.refuse);
    const taken = [];
    let failure = "";
    let pending = found.paths.length;
    const decide = () => {
      if (gone) return;
      const open = taken.filter(({ socket }) => !socket.destroyed);
      if (open.length === 1) return relay(open[0].socket);
      for (const { socket } of open) socket.destroy();
      if (open.length > 1) {
        return stop(`${open.length} sessions serve a view endpoint in ${target}: ${open.map(({ path }) => path).join(", ")}. Start the bridge with one of them: --connect <socket>`);
      }
      if (failure) return stop(failure);
      wait(found.paths.length === 0 ? found.none : `no running session: nothing listens on ${found.paths.join(", ")}`);
    };
    if (pending === 0) return decide();
    for (const path of found.paths) {
      const socket = createConnection(path);
      page.sockets.add(socket);
      let settled = false;
      const settle = () => {
        if (settled) return;
        settled = true;
        if (--pending === 0) decide();
      };
      socket.on("connect", () => {
        taken.push({ socket, path });
        settle();
      });
      socket.on("error", (err) => {
        if (!settled && err.code !== "ECONNREFUSED" && err.code !== "ENOENT") failure = `${path}: ${err.message}`;
      });
      socket.on("close", () => {
        page.sockets.delete(socket);
        settle();
      });
    }
  };
  // relay is the life of the page's connection: the page is told the session it posts to, the session's lines go to
  // the page, and the connection's end starts the next try.
  const relay = (socket) => {
    const id = randomBytes(8).toString("hex");
    let used = false; // the session wrote a line on this connection
    let inLine = false; // the page was written the start of a line and not yet its end
    sessions.set(id, { deliver: (line) => forward(socket, line), close: () => socket.destroy() });
    told = "";
    res.write(`event: session\ndata: ${id}\n\n`);
    socket.on("data", (chunk) => {
      // The bridge never stops reading the session, so the session never waits for a page. A page that does not read
      // is owed more and more instead, and is cut at the bound; its browser connects again and attaches afresh.
      if (res.writableLength >= QUEUE_BYTES) {
        leave();
        return res.destroy();
      }
      // The bytes go on as they come, without holding a line: `data: ` opens an event at the first byte of a line and
      // a blank line closes it at the line's end.
      const parts = [];
      for (let from = 0; from < chunk.length; ) {
        const end = chunk.indexOf(10, from);
        if (!inLine) parts.push(DATA);
        inLine = end < 0;
        parts.push(chunk.subarray(from, inLine ? chunk.length : end));
        if (!inLine) parts.push(BLANK);
        from = inLine ? chunk.length : end + 1;
        used ||= !inLine;
      }
      res.write(Buffer.concat(parts));
    });
    socket.on("close", () => {
      sessions.delete(id);
      if (gone) return;
      // A line the session did not finish is no line: it ends as an event the page does not listen to.
      if (inLine) res.write("\nevent: partial\n\n");
      // A connection the session wrote no line on, as when it has no room for one more, does not start the wait anew.
      if (used) deadline = Date.now() + waitMs;
      told = "the session closed the connection: another session took its place, or it ended";
      event("end", { retry: true, reason: told });
      wait(told);
    });
  };
  res.on("close", leave);
  event("endpoint", { state: "connecting", reason: `connecting to the session at ${target}` });
  connect();
}

// send passes on the one line a page posted.
function send(req, res, session) {
  const chunks = [];
  let size = 0;
  req.on("data", (chunk) => {
    size += chunk.length;
    if (size <= MAX_LINE_BYTES) chunks.push(chunk);
  });
  req.on("end", () => {
    const line = Buffer.concat(chunks).toString("utf8").replace(/\n$/, "");
    if (size > MAX_LINE_BYTES) return refuse(res, 413, "a line is at most 4 MiB");
    if (line === "" || line.includes("\n")) return refuse(res, 400, "the body is one line");
    const refused = session.deliver(line);
    if (refused) return refuse(res, ...refused);
    res.writeHead(204);
    res.end();
  });
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1");
  if (!own(req)) return refuse(res, 403, "not this page's request");
  if (req.method === "GET" && files[url.pathname]) {
    const [name, type] = files[url.pathname];
    let body = await readFile(fileURLToPath(new URL(name, import.meta.url)));
    // The theme's stylesheet comes last in the head, so its rules follow the page's own.
    if (theme && name === "index.html") body = body.toString("utf8").replace("</head>", `${THEME_LINK}</head>`);
    res.writeHead(200, { "content-type": type, "cache-control": "no-store" });
    return res.end(body);
  }
  if (req.method === "GET" && theme && url.pathname.startsWith("/theme/")) {
    const name = /^\/theme\/([A-Za-z0-9][A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]+)*)\.([a-z0-9]+)$/.exec(url.pathname);
    const body = name && THEME_TYPES[name[2]] ? await readFile(join(theme, `${name[1]}.${name[2]}`)).catch(() => null) : null;
    if (!body) return refuse(res, 404, "not found");
    res.writeHead(200, { "content-type": THEME_TYPES[name[2]], "cache-control": "no-store" });
    return res.end(body);
  }
  if (url.searchParams.get("token") !== token) return refuse(res, 403, "the token is missing or wrong");
  if (req.method === "GET" && url.pathname === "/events") return target ? follow(req, res) : events(req, res);
  if (req.method === "POST" && url.pathname === "/send") {
    const session = sessions.get(url.searchParams.get("session"));
    if (!session) return refuse(res, 404, "no such session");
    return send(req, res, session);
  }
  // What the bridge holds, by its own count and by Node's: the sessions pages post to, the pages with an open stream,
  // and every resource that keeps the process running (a retry timer is a Timeout, a connection to a session a PipeWrap).
  if (req.method === "GET" && url.pathname === "/state") {
    res.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
    return res.end(JSON.stringify({ sessions: sessions.size, pages: pages.size, resources: process.getActiveResourcesInfo() }) + "\n");
  }
  refuse(res, 404, "not found");
});

server.listen(port, "127.0.0.1", () => {
  console.log(`ui-view-web: http://127.0.0.1:${server.address().port}/?token=${token}`);
});
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    for (const session of sessions.values()) session.close();
    process.exit(0);
  });
}
