// Prints, as JSON, the fingerprint Pi computes for each contract of the cases below (Pi's `local` and `queries`).
// Usage: node --disable-warning=ExperimentalWarning queries_fingerprint_oracle.mjs <root of a Pi source tree holding packages/chord/src>
// The pins of queries_fingerprint_test.go come from this script run against Pi 0db9861ec018 (packages/chord/src/contracts).
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const root = process.argv[2];
const { local, evolve, queries, keyed, event, method, obj, str, int, arr, opt } = await import(
	pathToFileURL(join(root, "packages/chord/src/contracts/index.ts")).href
);

const note = () => obj({ text: str() });
const declared = (members, version = 1) => local("app.store", { version, params: {}, members });
const store = (members, version = 1) => declared(members, version).fingerprint;
const ofBoard = () => obj({ board: str() });
const one = () => obj({ id: int() });

const out = {
	noQueries: store({ notes: note(), add: method() }),
	keyedOnly: store({ notes: keyed(note()) }),
	eventOnly: store({ ping: event(str()) }),
	evolvedNoQueries: evolve(declared({ notes: note(), add: method() }), (e) => e.addMethod("clear", str(), str())).fingerprint,
	evolvedWithQueries: evolve(declared({ notes: queries(note(), { ofBoard: ofBoard(), one: one() }), add: method() }), (e) =>
		e.addMethod("clear", str(), str()),
	).fingerprint,
	noNamedQuery: store({ notes: queries(note(), {}) }),
	oneNoArgs: store({ notes: queries(note(), { all: obj({}) }) }),
	oneWithArgs: store({ notes: queries(note(), { ofBoard: ofBoard() }) }),
	twoOfBoardFirst: store({ notes: queries(note(), { ofBoard: ofBoard(), one: one() }) }),
	twoOneFirst: store({ notes: queries(note(), { one: one(), ofBoard: ofBoard() }) }),
	optionalArg: store({ notes: queries(note(), { page: obj({ board: str(), after: opt(str()) }) }) }),
	itemShape: store({ notes: queries(obj({ text: str(), tags: arr(str()) }), { all: obj({}) }) }),
	withOthers: store({
		events: event(str()),
		notes: queries(note(), { ofBoard: ofBoard(), one: one() }),
		add: method(note(), str()),
		drafts: keyed(note()),
		title: str(),
		other: queries(str(), { z: obj({}), a: obj({ k: int() }) }),
	}),
};
console.log(JSON.stringify(out, null, 1));
