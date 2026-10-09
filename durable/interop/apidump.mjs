// A canonical dump of a store through the Harness API: conversations, their entries and documents, tasks.
import { AgentDoc, LiveDoc, UsageDoc } from "@earendil-works/pi-durable";
import { canonical } from "./rawdump.mjs";
import { context, Notes, open, Plan, Sessions } from "./app.mjs";

export async function apiDump(harness) {
	const conversations = (await harness.commit((tx) => tx.scanConversations({}, 100), context)).items;
	const out = { conversations: [], tasks: [], sessions: await harness.snapshot(Sessions, context) };
	for (const record of conversations) {
		const conversation = await harness.conversation(record.id, context);
		const entries = (await conversation.entries({}, 1000, undefined, context)).items;
		const view = await conversation.context(context);
		out.conversations.push({
			record,
			entries,
			messages: view.messages,
			agent: await harness.snapshot(AgentDoc, record.id, context),
			live: await harness.snapshot(LiveDoc, record.id, context),
			usage: await harness.snapshot(UsageDoc, record.id, context),
			notes: await harness.snapshot(Notes, record.id, context),
			plan: await harness.snapshot(Plan, record.id, context),
		});
	}
	out.tasks = (await harness.commit((tx) => tx.scanTasks({}, 1000), context)).items;
	return canonical(JSON.parse(JSON.stringify(out)));
}

if (process.argv[1].endsWith("apidump.mjs")) {
	const { harness } = await open(process.argv[2]);
	process.stdout.write(`${JSON.stringify(await apiDump(harness), null, 1)}\n`);
	await harness.close(context);
}
