// The second scripted session: an agent configuration, a rewindable document, a document family member, a tool that
// runs a task-owned child conversation, a model error, and a fork as of an earlier entry.
import { Blob, context, model, open, Plan, say } from "./app.mjs";

const { harness } = await open(process.argv[2]);
const root = await harness.root(context, { agent: { model } });
await root.configure({ instructions: "Be brief", cwd: "/work" }, context);
await harness.commit(async (tx) => {
	(await tx.doc(Plan, root.id)).steps.push("one");
	(await tx.doc(Blob, root.id, "a1", { content: "v1" })).content = "v2";
}, context);
await say(root, "hello");
await harness.commit(async (tx) => {
	(await tx.doc(Plan, root.id)).steps.push("two");
}, context);
await say(root, "delegate this");
await say(root, "boom");
const entries = (await root.entries({}, 100, undefined, context)).items;
const at = entries[entries.length - 1].id;
const fork = await root.fork(at, { ownership: { kind: "ownerless" } }, context);
await say(fork, "fork turn");
for (const task of (await harness.inspect(context)).tasks) await harness.waitForTask(task.record.id, context);
await harness.close(context);
