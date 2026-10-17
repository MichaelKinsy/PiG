// Open each session file in the installed Pi's SessionManager and report what a caller observes: every read accessor, the files that branching, forking, appending and migration write, with new ids and clocks replaced by placeholders. session_oracle_test.go runs PiG's SessionManager through the same steps and compares the reports byte for byte.
import { copyFileSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SessionManager } = await import(pathToFileURL(root + "dist/index.js"));

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

// The tree as rows in depth-first order, each naming its children's ids: a 46,000-entry conversation nests 46,000 deep, which neither a recursive walk nor JSON.stringify survives.
const tree = (roots) => {
  const rows = [];
  const pending = [...roots].reverse();
  while (pending.length > 0) {
    const node = pending.pop();
    rows.push({ id: node.entry.id, label: node.label ?? null, labelTimestamp: node.labelTimestamp ?? null, children: node.children.map((child) => child.entry.id) });
    for (let i = node.children.length - 1; i >= 0; i--) pending.push(node.children[i]);
  }
  return rows;
};
const view = (sm) => {
  const entries = sm.getEntries();
  const leaves = entries.filter((e) => sm.getChildren(e.id).length === 0).map((e) => e.id);
  const projection = sm.buildSessionProjection();
  return {
    header: JSON.stringify(sm.getHeader()),
    entries: entries.map((e) => JSON.stringify(e)),
    leafId: sm.getLeafId(),
    sessionName: sm.getSessionName() ?? null,
    labels: entries.map((e) => sm.getLabel(e.id) ?? null),
    tree: JSON.stringify(tree(sm.getTree())),
    branch: sm.getBranch().map((e) => e.id),
    leafBranches: leaves.map((id) => sm.getBranch(id).map((e) => e.id)),
    contextEntries: sm.buildContextEntries().map((e) => e.id),
    projection: JSON.stringify({ entries: projection.entries.map((p) => ({ sourceId: p.sourceEntry.id, messages: p.messages })), messages: projection.messages, thinkingLevel: projection.thinkingLevel, model: projection.model ?? null }),
    context: JSON.stringify(sm.buildSessionContext()),
  };
};

const results = [];
for (const scenario of JSON.parse(input)) {
  const { dir, source, middleId, cwd } = scenario;
  const fresh = (name) => {
    const path = join(dir, name);
    copyFileSync(source, path);
    return path;
  };
  const out = {};
  const opened = fresh("open.jsonl");
  out.open = view(SessionManager.open(opened, dir, cwd));
  out.openedFile = readFileSync(opened, "utf8");

  const branchedDir = join(dir, "branched");
  mkdirSync(branchedDir, { recursive: true });
  const branchedPath = SessionManager.open(fresh("branch-source.jsonl"), branchedDir, cwd).createBranchedSession(middleId);
  out.branchedFile = branchedPath ? readFileSync(branchedPath, "utf8") : null;

  const forkDir = join(dir, "fork");
  mkdirSync(forkDir, { recursive: true });
  const forked = SessionManager.forkFrom(fresh("fork-source.jsonl"), cwd, forkDir);
  out.forkView = view(forked);
  out.forkFile = readFileSync(forked.getSessionFile(), "utf8");

  const appended = fresh("append.jsonl");
  const sm = SessionManager.open(appended, dir, cwd);
  const first = sm.getEntries()[0]?.id;
  if (first) sm.appendLabelChange(first, "first <label> & é");
  sm.branch(middleId);
  sm.appendMessage({ role: "user", content: [{ type: "text", text: "after the branch \u2028 <b>" }], timestamp: 1700000000123 });
  sm.appendSessionInfo("renamed \"session\"");
  out.appendView = view(sm);
  out.appendFile = readFileSync(appended, "utf8");
  results.push(out);
}
process.stdout.write(JSON.stringify(results));
