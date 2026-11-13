#!/usr/bin/env node
// A bridge between a browser and `pig --mode rpc`, for the plugin UI carrier. A browser cannot speak lines on stdin and
// stdout, so this program starts pig as a child for every page that connects, relays the lines pig writes as Server-Sent
// Events and the lines the page POSTs to pig's stdin, and serves the page. It uses Node's standard library only and is
// not part of pig: pig gains no web server.
//
//   node bridge.mjs [--port <n>] [--pig <path>] [-- <pig arguments>]
//
// It listens on 127.0.0.1 only and prints the page's address, which carries a token every request must repeat.
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";

// A command line of the carrier is at most 4 MiB; the bridge reads no more than that from a page.
const MAX_LINE_BYTES = 4 << 20;

const argv = process.argv.slice(2);
const rest = argv.includes("--") ? argv.splice(argv.indexOf("--")).slice(1) : [];
const option = (name, fallback) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : fallback);
const port = Number(option("--port", "0"));
const pig = option("--pig", process.env.PIG_BIN || "pig");
const token = randomBytes(16).toString("hex");
const files = { "/": ["index.html", "text/html; charset=utf-8"], "/client.js": ["client.js", "text/javascript; charset=utf-8"] };
const sessions = new Map();

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
  sessions.set(id, child);
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

// send writes the one line a page posted to its pig.
function send(req, res, child) {
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
    child.stdin.write(line + "\n");
    res.writeHead(204);
    res.end();
  });
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1");
  if (!own(req)) return refuse(res, 403, "not this page's request");
  if (req.method === "GET" && files[url.pathname]) {
    const [name, type] = files[url.pathname];
    const body = await readFile(fileURLToPath(new URL(name, import.meta.url)));
    res.writeHead(200, { "content-type": type, "cache-control": "no-store" });
    return res.end(body);
  }
  if (url.searchParams.get("token") !== token) return refuse(res, 403, "the token is missing or wrong");
  if (req.method === "GET" && url.pathname === "/events") return events(req, res);
  if (req.method === "POST" && url.pathname === "/send") {
    const child = sessions.get(url.searchParams.get("session"));
    if (!child) return refuse(res, 404, "no such session");
    return send(req, res, child);
  }
  refuse(res, 404, "not found");
});

server.listen(port, "127.0.0.1", () => {
  console.log(`ui-view-web: http://127.0.0.1:${server.address().port}/?token=${token}`);
});
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    for (const child of sessions.values()) child.kill();
    process.exit(0);
  });
}
