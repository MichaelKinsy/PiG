// Continues a session another runtime started: reopen, answer new input, fork again, compact, answer once more.
import { context, model, open, say } from "./app.mjs";

const { harness } = await open(process.argv[2]);
const root = await harness.root(context, { agent: { model } });
harness.resume();
await say(root, "after reopen");
const conversations = (await harness.commit((tx) => tx.scanConversations({}, 100), context)).items;
const forked = conversations.find((record) => record.id !== root.id);
if (forked === undefined) throw new Error("the store has no fork");
await say((await harness.conversation(forked.id, context)), "fork again");
const compaction = await root.compact("keep notes again", context);
await harness.waitForTask(compaction, context);
await say(root, "last");
await harness.close(context);
