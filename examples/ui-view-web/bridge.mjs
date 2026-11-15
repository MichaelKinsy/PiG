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
// It listens on 127.0.0.1 only. The address is good for ONE load. It holds a one-time code in its fragment
// (`http://127.0.0.1:<port>/#<code>`), which no browser sends to a server, in a request line or in a Referer. The page
// posts the code to /exchange (in the body, never in an address) and is answered with a session secret, which it keeps in
// sessionStorage (one tab, one origin, so one port) and sends in an Authorization header with every request. A request
// but the page's own files and /exchange without that secret is refused; no address holds a secret after the load and the
// bridge sets no cookie (a cookie of 127.0.0.1:<port> would go to every other port of the host).
// The address is not printed, so a log, a recording or a shared screen holds no live address: the first line is
// `ui-view-web: http://127.0.0.1:<port>/ (open: <dir>/open.html)`, and open.html (mode 0600, in a directory of mode 0700,
// removed when the bridge ends) sends a browser to the address. Open that file, never the address: no argument list then
// holds the code. The file `address` beside it holds the address for a script. A code is good for ten minutes and one
// exchange; a use or the ten minutes make the next code, written to both files at once. --print-token, or
// UI_VIEW_WEB_PRINT_TOKEN=1, prints `ui-view-web: http://127.0.0.1:<port>/#<code>` instead and makes no file: that one
// code is good for one exchange and ten minutes, and there is no next one. With `-- <pig arguments>` the secret lets a
// process of this machine prompt the pig the bridge starts, as this user: it is in the tab's sessionStorage and nowhere else.
import { spawn } from "node:child_process";
import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import { mkdtempSync, readdirSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { createConnection } from "node:net";
import { tmpdir } from "node:os";
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
// --print-token, or UI_VIEW_WEB_PRINT_TOKEN=1 (exactly 1), prints the address with its code. Without either the address
// is written to a private file and the printed line names the file.
const printToken = argv.includes("--print-token") || process.env.UI_VIEW_WEB_PRINT_TOKEN === "1";
// A code lasts ten minutes and one use, as the owner link of examples/chord-todo does (chord/nextnode/app/owner.go,
// ownerLinkMs): the same one-time secret in a fragment, posted by the page.
const CODE_MS = 10 * 60_000;
// A body of /exchange is {"code": "<32 hex digits>"}.
const MAX_EXCHANGE_BYTES = 4096;
// The session secrets the bridge keeps are the newest 64: a local caller that holds codes cannot grow the map, and the oldest
// secret is dropped (a page that holds it is told to open the address again) when one more is made.
const MAX_SECRETS = 64;
const files = { "/": ["index.html", "text/html; charset=utf-8"], "/client.js": ["client.js", "text/javascript; charset=utf-8"] };
// Where the lines of a page go, by the session it was told: deliver(line) passes one line on or returns the refusal
// [status, text], and close() ends the session.
const sessions = new Map();
// The pigs this bridge started and that have not ended.
const children = new Set();
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
  children.add(child);
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
  child.on("error", (err) => {
    children.delete(child);
    res.end(`event: end\ndata: ${JSON.stringify({ error: String(err) })}\n\n`);
  });
  child.on("exit", (code) => {
    children.delete(child);
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

// The page open.html is: it sends the browser on to the address and is not kept in its history. For a browser that does
// not follow a script from a local file, it also shows a link to click; the code is then in the page of a private file
// and in no argument list.
function openPage(address) {
  const escaped = address.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
  const script = JSON.stringify(address).replace(/</g, "\\u003c");
  return `<!doctype html>\n<meta charset="utf-8">\n<meta name="referrer" content="no-referrer">\n<title>ui-view-web</title>\n<script>location.replace(${script});</script>\n<p><a href="${escaped}" rel="noreferrer">Open the page</a></p>\n`;
}

const sha256 = (text) => createHash("sha256").update(text).digest("hex");
// The session secrets, by their digest: a lookup then compares hashes, not the secret. The Map keeps its order, so the
// first key is the oldest.
const secrets = new Map();
function newSecret() {
  const secret = randomBytes(32).toString("hex");
  secrets.set(sha256(secret), true);
  if (secrets.size > MAX_SECRETS) secrets.delete(secrets.keys().next().value);
  return secret;
}
function holdsSecret(req) {
  const given = /^Bearer ([0-9a-f]{64})$/.exec(req.headers.authorization ?? "");
  return given !== null && secrets.has(sha256(given[1]));
}

// The one live code, or none (after --print-token's one use). publish writes it where an author reads it.
let live = null;
let publish = () => {};
let renewal;
function mint() {
  live = { code: randomBytes(16).toString("hex"), expires: Date.now() + CODE_MS };
}
// renew makes the next code and writes it; the timer makes another if nobody used this one. It is not kept by the
// process: the server is what keeps the bridge running.
function renew() {
  mint();
  publish(live.code);
  clearTimeout(renewal);
  renewal = setTimeout(renew, CODE_MS);
  renewal.unref();
}
// consume says whether given is the live code and it had not expired. A code that matches is used up either way, and its
// check and its use are one synchronous step, so of two requests for one code only one is answered with a secret. A
// request with a wrong code uses nothing. The next code is written before the answer: a script that reads the file
// after it has been answered reads a live code.
function consume(given) {
  if (live === null) return false;
  const want = Buffer.from(live.code);
  const got = Buffer.from(given);
  if (got.length !== want.length || !timingSafeEqual(got, want)) return false;
  const fresh = Date.now() < live.expires;
  live = null;
  if (!printToken) renew();
  return fresh;
}

// exchange trades the live code, posted in the body by the page, for a session secret.
function exchange(req, res) {
  const chunks = [];
  let size = 0;
  req.on("data", (chunk) => {
    size += chunk.length;
    if (size <= MAX_EXCHANGE_BYTES) chunks.push(chunk);
  });
  req.on("end", () => {
    if (size > MAX_EXCHANGE_BYTES) return refuse(res, 413, "the body is at most 4 KiB");
    let given;
    try {
      given = JSON.parse(Buffer.concat(chunks).toString("utf8"))?.code;
    } catch {
      given = undefined;
    }
    if (typeof given !== "string") return refuse(res, 400, 'the body is {"code": "<the code>"}');
    if (!consume(given)) return refuse(res, 403, "the code is used up, expired or wrong");
    res.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
    res.end(JSON.stringify({ secret: newSecret() }) + "\n");
  });
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1");
  // No page of the bridge is kept, and none is a referrer: a link a view draws or a resource a theme names is not told
  // the address of the page.
  res.setHeader("cache-control", "no-store");
  res.setHeader("referrer-policy", "no-referrer");
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
  if (req.method === "POST" && url.pathname === "/exchange") return exchange(req, res);
  if (!holdsSecret(req)) return refuse(res, 403, "the secret is missing or wrong");
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
  const origin = `http://127.0.0.1:${server.address().port}`;
  if (printToken) {
    mint();
    return console.log(`ui-view-web: ${origin}/#${live.code}`);
  }
  // A log, a terminal recording or a shared screen must not hold a live address: the line names a file to open instead.
  // The directory is made private (mkdtemp: mode 0700) and the files in it are 0600; all are removed when the process
  // ends, by any path that runs code ('exit' follows a signal handler's process.exit and an uncaught error). On Windows
  // the mode bits do not apply: what protects the files is the ACL the user's temporary directory gives what is made in it.
  const dir = mkdtempSync(join(tmpdir(), "ui-view-web-"));
  process.on("exit", () => rmSync(dir, { recursive: true, force: true }));
  const open = join(dir, "open.html");
  // address holds the address and nothing else, for a script that reads it. It is never an argument of a command: a
  // command that gave the browser the address would put the code in the argument list of the opener and of the browser,
  // which every user of a Linux machine can read (/proc/<pid>/cmdline). open.html is what an author opens: its path holds
  // no code, and the file is as private as address. Each is written to a temporary file and renamed over the old one, so
  // a reader sees a whole file, and the directory holds those two files and nothing else.
  publish = (code) => {
    const address = `${origin}/#${code}`;
    for (const [name, text] of [["address", address], ["open.html", openPage(address)]]) {
      const temporary = join(dir, `.${name}.new`);
      writeFileSync(temporary, text, { mode: 0o600 });
      renameSync(temporary, join(dir, name));
    }
  };
  renew();
  console.log(`ui-view-web: ${origin}/ (open: ${open})`);
});
// A signal ends the sessions and then waits for the pigs it started: a pig's own teardown (it removes the files it made in
// the temporary directory) runs after its stdin or its signal, and the bridge's exit must not outrun it.
for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(signal, () => {
    for (const session of sessions.values()) session.close();
    const killer = setTimeout(() => children.forEach((child) => child.kill("SIGKILL")), 10000);
    Promise.all([...children].map((child) => new Promise((resolve) => child.once("close", resolve)))).then(() => {
      clearTimeout(killer);
      process.exit(0);
    });
  });
}
