// The scripted session: documents, a tool call that starts a task, a fork with an init, compaction, a reset.
import { context, model, Notes, open, say, Sessions } from "./app.mjs";

const path = process.argv[2];
const { harness } = await open(path);
const root = await harness.root(context, { agent: { model } });
await harness.commit(async (tx) => {
	(await tx.doc(Sessions)).items["1"] = { title: "first", cwd: "/work" };
}, context);

await say(root, "hello");
// The job the tool created is the store's only task so far; wait for it.
for (const task of (await harness.inspect(context)).tasks) await harness.waitForTask(task.record.id, context);

const answer = (await root.entries({}, 50, undefined, context)).items[0];
const fork = await root.fork(
	answer.id,
	{
		ownership: { kind: "ownerless" },
		init: async (tx, id) => {
			(await tx.doc(Notes, id)).lines.push("forked");
		},
	},
	context,
);
await say(fork, "second");
await say(root, "third");
const compaction = await root.compact("keep notes", context);
await harness.waitForTask(compaction, context);
await root.reset("handoff", context);
await say(root, "after reset");
await harness.close(context);
