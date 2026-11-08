// Ports test/p2/voice.test.ts (Pi main c23b9ec1): the cases of voice mode's own TypeScript, the browser's half that Pi
// ships and p2 carries verbatim (web/upstream/plugins/voice): sentences of streaming Markdown, stop commands, transcripts
// waiting for the conversation, pibot's barge-in detector, interruptions, microphone resampling, answers read while they
// stream, room frames, the agent's name spoken as a mention, and the voice choice against the service. Each case is Pi's
// text with its title, run on the vendored sources by plugins/voice/web_test.go (oracle.RunNodeCases); the cases that need
// the server (the room bridge, the voice.json watch, the switch, the prompt section and its language) are the Go tests
// beside it and in e2e/voice_test.go.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { describe, it } from "node:test";
import { ChordError } from "@earendil-works/chord";
import type { ConversationView } from "@earendil-works/pi-durable";
import { spokenMention } from "../../web/upstream/plugins/chat-mode/contract.ts";
import { BargeInDetector, type BargeInPhase, Pcm16kResampler } from "../../web/upstream/plugins/voice/barge-in.ts";
import { languagesOf, resolved } from "../../web/upstream/plugins/voice/choice.ts";
import { responses } from "../../web/upstream/plugins/voice/engine.ts";
import {
	CANDIDATE_START_MS,
	Interruption,
	type InterruptionState,
	isEcho,
	meaningful,
} from "../../web/upstream/plugins/voice/interruption.ts";
import { bargeInPhase } from "../../web/upstream/plugins/voice/policy.ts";
import { parseRoomFrame, roomName } from "../../web/upstream/plugins/voice/protocol.ts";
import { isStopCommand, SpeechChunker, speakable } from "../../web/upstream/plugins/voice/speech-text.ts";
import {
	PendingTranscripts,
	type TranscriptTarget,
	voiceMessage,
} from "../../web/upstream/plugins/voice/transcripts.ts";

/** The fake speech service's engines (test/p2/voice-service.ts `VOICES`): the file the Go tests' service answers `/voices` with. */
const VOICES = JSON.parse(readFileSync(new URL("voicetest/voices.json", import.meta.url), "utf8"));

/** Waits until `check` holds (test/p2/helpers.ts `poll`). */
async function poll(check: () => boolean | Promise<boolean>, timeoutMs = 20_000, what = "condition"): Promise<void> {
	const deadline = Date.now() + timeoutMs;
	while (!(await check())) {
		if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
		await new Promise((resolve) => setTimeout(resolve, 25));
	}
}

describe("voice speech text", () => {
	it("reads streaming Markdown sentence by sentence, without code blocks, tables or syntax", () => {
		const chunker = new SpeechChunker();
		const text =
			"I found it. The bug is in `tx.draft()`, see [the docs](https://x.y/z). Next\n\n```ts\nconst a = 1. b;\n```\n| a | b |\n|---|---|\n- **First** item\n1. Second, e.g. this\n## Done\nRead https://example.com now";
		const said: string[] = [];
		// Fed in growing prefixes, as the answer streams.
		for (let end = 1; end <= text.length; end += 7) said.push(...chunker.push(text.slice(0, end)));
		said.push(...chunker.push(text, true));
		assert.deepEqual(said, [
			"I found it.",
			"The bug is in tx.draft(), see the docs.",
			"Next",
			"First item",
			"Second, e.g. this",
			"Done",
			"Read a link now",
		]);
	});

	it("waits for a sentence to end before reading it, and for a code fence to close", () => {
		const chunker = new SpeechChunker();
		assert.deepEqual(chunker.push("Hello wor"), []);
		assert.deepEqual(chunker.push("Hello world. And"), ["Hello world."]);
		assert.deepEqual(chunker.push("Hello world. And then\n```\ncode. more"), ["And then"]);
		assert.deepEqual(chunker.push("Hello world. And then\n```\ncode. more\n```\nAfter."), []);
		assert.deepEqual(chunker.push("Hello world. And then\n```\ncode. more\n```\nAfter.", true), ["After."]);
		assert.equal(speakable("---"), "");
		assert.equal(speakable("| x |"), "");
	});

	it("recognizes stop commands, not sentences containing stop", () => {
		for (const text of ["Stop.", "stop it", "OK, stop!", "Never mind", "Be quiet."])
			assert(isStopCommand(text), text);
		for (const text of ["Stop the server and restart it.", "Don't stop", "stopwatch"])
			assert(!isStopCommand(text), text);
	});
});

/** A conversation whose sends settle when the test says so, recording what happened in order. */
function fakeTarget() {
	const calls: string[] = [];
	const restore: string[] = [];
	const requests = new Map<string, string>();
	const pending: { resolve: () => void; reject: (error: unknown) => void }[] = [];
	let inFlight = 0;
	let maxInFlight = 0;
	const target: TranscriptTarget = {
		send: (text, requestId) => {
			calls.push(`send ${text}`);
			const known = requests.get(text);
			if (known !== undefined) assert.equal(requestId, known, "a retry keeps its request ID");
			requests.set(text, requestId);
			inFlight++;
			maxInFlight = Math.max(maxInFlight, inFlight);
			return new Promise<void>((resolve, reject) => {
				pending.push({
					resolve: () => {
						inFlight--;
						resolve();
					},
					reject: (error) => {
						inFlight--;
						reject(error);
					},
				});
			});
		},
		stop: async () => {
			calls.push("stop");
			// What the stop withdrew of this person's queued messages (once).
			return { restored: restore.splice(0) };
		},
	};
	/** The next send, once it is asked for. */
	const next = async () => {
		await poll(() => pending.length > 0, 2000, "a send");
		const first = pending.shift();
		assert(first);
		return first;
	};
	return { target, calls, next, restore, maxInFlight: () => maxInFlight };
}

describe("voice transcripts waiting for the conversation", () => {
	const setup = (options: { busy?: boolean; limit?: number } = {}) => {
		const fake = fakeTarget();
		let target: TranscriptTarget | undefined = fake.target;
		const errors: string[] = [];
		const queue = new PendingTranscripts({
			target: () => target,
			busy: () => options.busy ?? false,
			log: () => {},
			onError: (message) => errors.push(message),
			onChange: () => {},
			limit: options.limit,
			retryMs: 60_000,
			slowMs: 60_000,
		});
		const setTarget = (value: TranscriptTarget | undefined) => {
			target = value;
		};
		return { ...fake, queue, errors, setTarget };
	};

	it("sends one at a time in the order spoken, stopping the busy run before each, as the Stop button", async () => {
		const { queue, calls, next, maxInFlight } = setup({ busy: true });
		queue.push("one");
		queue.push("two");
		queue.push("three");
		(await next()).resolve();
		(await next()).resolve();
		(await next()).resolve();
		await queue.flush();
		assert.deepEqual(calls, ["stop", "send one", "stop", "send two", "stop", "send three"]);
		assert.equal(maxInFlight(), 1);
		assert.equal(queue.size, 0);
	});

	it("sends this person's queued messages that a stop withdrew with the new one, not lost", async () => {
		const { queue, calls, next, restore } = setup({ busy: true });
		restore.push("first thought");
		queue.push("second thought");
		(await next()).resolve();
		await queue.flush();
		assert.deepEqual(calls, ["stop", "send first thought\n\nsecond thought"]);
	});

	it("does not stop again when a message is sent again after a link failure", async () => {
		const { queue, calls, next, setTarget, target } = setup({ busy: true });
		queue.push("once");
		(await next()).reject(new ChordError({ code: "unavailable", message: "ping timeout" }));
		setTarget(target);
		const flushed = queue.flush();
		(await next()).resolve();
		await flushed;
		assert.deepEqual(calls, ["stop", "send once", "send once"]);
	});

	it("keeps transcripts while the link is down and sends them once through the new reference", async () => {
		const { queue, calls, next, errors, setTarget, target } = setup();
		queue.push("first");
		// The link dies under the first send (a ping timeout): kept, with its request ID.
		(await next()).reject(new ChordError({ code: "unavailable", message: "link to session:x closed: ping timeout" }));
		setTarget(undefined);
		await queue.flush();
		queue.push("second");
		await queue.flush();
		assert.equal(queue.size, 2);
		assert.equal(queue.waiting, 2);
		// The replacement reference arrives (rebind): both go, in order, the first with its old request ID.
		setTarget(target);
		const flushed = queue.flush();
		(await next()).resolve();
		(await next()).resolve();
		await flushed;
		assert.deepEqual(calls, ["send first", "send first", "send second"]);
		assert.equal(queue.size, 0);
		assert.equal(queue.waiting, 0);
		assert.deepEqual(errors, []);
	});

	it("drops a refused transcript with an error, and the oldest waiting beyond the limit", async () => {
		const { queue, next, errors, setTarget } = setup({ limit: 2 });
		queue.push("refused");
		(await next()).reject(new ChordError({ code: "forbidden", message: "no" }));
		await queue.flush();
		assert.equal(queue.size, 0);
		// Chord v2 error messages carry no code prefix.
		assert.match(errors[0] ?? "", /Send failed: no$/);
		setTarget(undefined);
		queue.push("a");
		queue.push("b");
		queue.push("c");
		assert.equal(queue.size, 2);
		assert.match(errors[1] ?? "", /Too many messages waiting/);
		queue.clear();
	});
});

describe("voice barge-in detector (pibot's)", () => {
	const rate = 16000;
	const frame = 1024;
	const tone = (start: number, length: number) =>
		Float32Array.from({ length }, (_, index) => 0.5 * Math.sin((2 * Math.PI * 300 * (start + index)) / rate));
	let seed = 1;
	const noise = (length: number, amplitude: number) =>
		Float32Array.from({ length }, () => {
			seed = (seed * 16807) % 2147483647;
			return (seed / 2147483647 - 0.5) * 2 * amplitude;
		});
	const pcmOf = (input: Float32Array) => Int16Array.from(input, (sample) => Math.round(sample * 0x7fff));
	/** Plays `seconds` of the tone into the reference; returns how many samples played. */
	const playing = (detector: BargeInDetector, seconds: number) => {
		for (let at = 0; at < seconds * rate; at += frame) detector.handlePlaybackAudio(tone(at, frame), rate);
		return Math.ceil((seconds * rate) / frame) * frame;
	};
	/** Feeds `frames` frames made by `make`; the first triggering result, if any. */
	const feed = (detector: BargeInDetector, frames: number, make: () => Float32Array, phase: BargeInPhase) => {
		for (let index = 0; index < frames; index++) {
			const input = make();
			const result = detector.observeMic(input, rate, pcmOf(input), phase, true);
			if (result.triggered) return result;
		}
		return undefined;
	};

	it("streams normally only while listening or hearing, and buffers for barge-in otherwise", () => {
		const detector = new BargeInDetector(rate);
		assert.equal(detector.shouldStreamMicNormally("listening"), true);
		assert.equal(detector.shouldStreamMicNormally("hearing"), true);
		for (const phase of ["thinking", "tool", "speaking", "inactive", "error"] as const)
			assert.equal(detector.shouldStreamMicNormally(phase), false);
		// A loud voice while listening is never a barge-in (it is streamed anyway).
		assert.equal(
			feed(detector, 20, () => noise(frame, 0.5), "listening"),
			undefined,
		);
	});

	it("takes the playback's own echo for echo while speaking", () => {
		const detector = new BargeInDetector(rate);
		const end = playing(detector, 1);
		// The microphone hears the playback 100 ms late, frame after frame.
		let at = end - 10 * frame - 0.1 * rate;
		assert.equal(
			feed(
				detector,
				10,
				() => {
					const echo = tone(at, frame);
					at += frame;
					return echo;
				},
				"speaking",
			),
			undefined,
		);
	});

	it("interrupts after five frames of sound the playback does not explain, then streams until reset", () => {
		const detector = new BargeInDetector(rate);
		playing(detector, 1);
		const result = feed(detector, 20, () => noise(frame, 0.5), "speaking");
		assert(result?.preroll && result.metrics);
		assert(result.metrics.residualRatio >= 0.62 && result.metrics.micRms >= 0.018);
		// The preroll is the last second of microphone audio (16 kHz).
		assert.equal(result.preroll.length, 5 * frame);
		assert.equal(detector.isStreaming(), true);
		detector.resetStreaming();
		assert.equal(detector.isStreaming(), false);
		// The next trigger's preroll holds only what was not streamed yet (noise triggering again and again must not
		// send more than real time).
		const again = feed(detector, 20, () => noise(frame, 0.5), "speaking");
		assert.equal(again?.preroll?.length, 5 * frame);
	});
});

describe("voice interruptions", () => {
	const rate = 16000;
	const frame = 1024;
	let seed = 7;
	const loud = () =>
		Float32Array.from({ length: frame }, () => {
			seed = (seed * 16807) % 2147483647;
			return (seed / 2147483647 - 0.5) * 1;
		});
	const pcmOf = (input: Float32Array) => Int16Array.from(input, (sample) => Math.round(sample * 0x7fff));

	it("listens for barge-in only while the agent's speech plays", () => {
		const detector = new BargeInDetector(rate);
		for (let at = 0; at < rate; at += frame) detector.handlePlaybackAudio(new Float32Array(frame).fill(0.1), rate);
		const feed = (playing: boolean) => {
			const phase = bargeInPhase({ hearing: false, playing });
			for (let index = 0; index < 20; index++) {
				const input = loud();
				if (detector.observeMic(input, rate, pcmOf(input), phase, playing).triggered)
					return { phase, barged: true };
			}
			return { phase, barged: false };
		};
		// The agent works silently: the speaker's words stream as usual, no barge-in.
		assert.deepEqual(feed(false), { phase: "listening", barged: false });
		assert.equal(detector.shouldStreamMicNormally(bargeInPhase({ hearing: false, playing: false })), true);
		// Its speech plays: sound it does not explain triggers (a candidate: interruption.ts decides).
		assert.deepEqual(feed(true), { phase: "speaking", barged: true });
		assert.equal(bargeInPhase({ hearing: true, playing: true }), "hearing");
	});

	it("a message sent while the agent works stops it first, unless the speaker's words stopped it already", async () => {
		const calls: string[] = [];
		let busy = true;
		const queue = new PendingTranscripts({
			target: () => ({
				send: async (text) => calls.push(`send ${text}`),
				stop: async () => {
					calls.push("stop");
					busy = false;
					return { restored: [] };
				},
			}),
			busy: () => busy,
			log: () => {},
			onError: () => {},
			onChange: () => {},
		});
		queue.push("wait, do it differently");
		await queue.flush();
		assert.deepEqual(calls, ["stop", "send wait, do it differently"]);
		busy = true;
		queue.push("already stopped by its words", false);
		await queue.flush();
		assert.deepEqual(calls.slice(2), ["send already stopped by its words"]);
	});

	it("waits for a stop in flight before sending, and sends the messages it withdrew with the next one", async () => {
		const calls: string[] = [];
		let settle: (value: { restored: readonly string[] }) => void = () => {};
		const queue = new PendingTranscripts({
			target: () => ({
				send: async (text) => calls.push(`send ${text}`),
				stop: async () => ({ restored: [] }),
			}),
			busy: () => false,
			log: () => {},
			onError: () => {},
			onChange: () => {},
		});
		queue.stopping(
			new Promise((resolve) => {
				settle = resolve;
			}),
		);
		queue.push("said after the interruption", false);
		await new Promise((resolve) => setTimeout(resolve, 20));
		assert.deepEqual(calls, [], "no send while the stop is in flight (it would withdraw it)");
		settle({ restored: ["queued before"] });
		await poll(() => calls.length === 1, 2000, "sent");
		assert.deepEqual(calls, ["send queued before\n\nsaid after the interruption"]);
	});

	it("keeps messages a barge-in's stop withdrew: with the next message, or alone after a while", async () => {
		const sent: string[] = [];
		const queue = new PendingTranscripts({
			target: () => ({
				send: async (text) => sent.push(text),
				stop: async () => ({ restored: [] }),
			}),
			busy: () => false,
			log: () => {},
			onError: () => {},
			onChange: () => {},
			carryMs: 50,
		});
		queue.carry(["queued before"]);
		queue.push("said now");
		await queue.flush();
		assert.deepEqual(sent, ["queued before\n\nsaid now"]);
		queue.carry(["nothing follows"]);
		await poll(() => sent.length === 2, 2000, "sent alone");
		await queue.flush();
		assert.equal(sent[1], "nothing follows");
	});
});

describe("voice interruption by words", () => {
	/** An Interruption with a scripted clock and engine state, recording what it did. */
	const setup = (state: Partial<Omit<InterruptionState, "said">> & { said?: string[] } = {}) => {
		const did: string[] = [];
		let now = 0;
		const current = { busy: true, speaking: true, chatMode: false, agentName: undefined, ...state };
		const interruption = new Interruption({
			state: () => ({ ...current, said: () => state.said ?? [] }),
			cutSpeech: () => did.push("cut speech"),
			stopRun: () => did.push("stop run"),
			dropStream: () => did.push("drop stream"),
			log: () => {},
			now: () => now,
		});
		return {
			interruption,
			did,
			current,
			advance: (ms: number) => {
				now += ms;
			},
		};
	};

	it("tells words from noise, fillers and the agent's own speech heard back", () => {
		for (const text of ["", " ", ".", "Mm.", "Uh, um.", "Hmm?", "A", "Oh. Ah."]) assert(!meaningful(text), text);
		for (const text of ["No.", "Stop", "Wait, use the other file.", "42", "Nein, warte."])
			assert(meaningful(text), text);
		const said = ["I will look at the build log and then restart the server."];
		assert(isEcho("look at the build log", said));
		assert(!isEcho("no, the test log", said), "a word the agent did not say");
		assert(!isEcho("the server", said), "two words are the speaker's");
	});

	it("drops a barge-in the room never hears as speech: the agent keeps speaking and working", () => {
		const { interruption, did, advance } = setup();
		interruption.trigger();
		assert.equal(interruption.pending, true);
		advance(CANDIDATE_START_MS - 1);
		interruption.tick();
		assert.deepEqual(did, []);
		advance(2);
		interruption.tick();
		assert.deepEqual(did, ["drop stream"]);
		assert.equal(interruption.pending, false);
	});

	it("drops a barge-in whose speech has no words (wipers): nothing cut, nothing sent", () => {
		for (const final of ["", "Mm.", "look at the build log"]) {
			const { interruption, did } = setup({ said: ["I will look at the build log."] });
			interruption.trigger();
			interruption.speechStart();
			interruption.tick();
			interruption.interim("Hmm.");
			assert.deepEqual(did, [], final);
			assert.deepEqual(interruption.final(final), { text: undefined, stopped: false }, final);
			assert.deepEqual(did, ["drop stream"], final);
		}
	});

	it("interrupts at the first words: speech cut and run stopped once, then the final is the message", () => {
		const { interruption, did } = setup();
		interruption.trigger();
		interruption.speechStart();
		assert.equal(interruption.pending, false, "the room heard speech: it is the speaker's utterance");
		interruption.interim("Wait");
		assert.deepEqual(did, ["cut speech", "stop run"]);
		interruption.interim("Wait, use the other file");
		assert.deepEqual(did, ["cut speech", "stop run"], "once per utterance");
		assert.deepEqual(interruption.final("Wait, use the other file."), {
			text: "Wait, use the other file.",
			stopped: true,
		});
		assert.deepEqual(did, ["cut speech", "stop run"]);
	});

	it("stops a silently working agent at the first words, without waiting for the end of the utterance", () => {
		const { interruption, did } = setup({ speaking: false });
		interruption.speechStart();
		interruption.interim("Um");
		assert.deepEqual(did, []);
		interruption.interim("Actually, no");
		assert.deepEqual(did, ["stop run"]);
	});

	it("interrupts at the final when the service sends no interim transcripts", () => {
		const { interruption, did } = setup();
		interruption.trigger();
		interruption.speechStart();
		assert.deepEqual(interruption.final("No, the other one."), { text: "No, the other one.", stopped: true });
		assert.deepEqual(did, ["cut speech", "stop run"]);
	});

	it("sends a dictation the service split as one message, never stopping the run of its first half", () => {
		// The service ends an utterance at 30 s and the next one starts before the first one's final arrives.
		const { interruption, did, current } = setup({ speaking: false, busy: false });
		interruption.speechStart();
		interruption.interim("First I want you to");
		interruption.speechStart();
		assert.deepEqual(interruption.final("First I want you to look at the logs,"), {
			text: undefined,
			stopped: false,
		});
		// The second half's words belong to the second utterance; nothing ran for the first, nothing is stopped.
		interruption.interim("and then");
		assert.deepEqual(interruption.final("and then restart it."), {
			text: "First I want you to look at the logs, and then restart it.",
			stopped: false,
		});
		assert.deepEqual(did, []);
		// While the agent works: the first half stops it once, the second half does not stop again.
		current.busy = true;
		interruption.speechStart();
		interruption.interim("No wait");
		interruption.speechStart();
		assert.deepEqual(interruption.final("No wait, not that one."), { text: undefined, stopped: false });
		interruption.interim("Use the other");
		assert.deepEqual(interruption.final("Use the other file."), {
			text: "No wait, not that one. Use the other file.",
			stopped: true,
		});
		assert.deepEqual(did, ["stop run"]);
		// A held half whose continuation is noise goes alone.
		interruption.speechStart();
		interruption.speechStart();
		assert.deepEqual(interruption.final("Check the build."), { text: undefined, stopped: false });
		assert.deepEqual(interruption.final(""), { text: "Check the build.", stopped: true });
	});

	it("in chat mode cuts the speech for anyone's words, and stops the run only when they are for the agent", () => {
		const chat = setup({ chatMode: true });
		chat.interruption.speechStart();
		chat.interruption.interim("lunch at noon?");
		assert.deepEqual(chat.did, ["cut speech"]);
		const addressed = setup({ chatMode: true, speaking: false });
		addressed.interruption.speechStart();
		addressed.interruption.interim("lunch at noon?");
		assert.deepEqual(addressed.did, []);
		assert.deepEqual(addressed.interruption.final("lunch at noon?"), { text: "lunch at noon?", stopped: false });
		addressed.interruption.speechStart();
		addressed.interruption.interim("Pi, stop that");
		assert.deepEqual(addressed.did, ["stop run"]);
		// People talking over the agent cut its speech; the agent's name later in the same utterance stops its run.
		const later = setup({ chatMode: true });
		later.interruption.speechStart();
		later.interruption.interim("lunch at noon?");
		later.interruption.interim("lunch at noon? Pi, stop that");
		assert.deepEqual(later.did, ["cut speech", "stop run"]);
		const stop = setup({ chatMode: true, speaking: false });
		stop.interruption.speechStart();
		stop.interruption.interim("Stop.");
		assert.deepEqual(stop.did, ["stop run"]);
	});

	it("forgets the utterances in progress when the room's connection ends, returning what was held", () => {
		const { interruption, did, advance } = setup();
		interruption.trigger();
		assert.deepEqual(interruption.reset(), { text: undefined, stopped: false });
		advance(CANDIDATE_START_MS * 2);
		interruption.tick();
		assert.deepEqual(did, []);
		assert.equal(interruption.pending, false);
		interruption.speechStart();
		interruption.speechStart();
		interruption.final("Keep this.");
		assert.deepEqual(interruption.reset(), { text: "Keep this.", stopped: true });
	});
});

describe("voice microphone resampling", () => {
	it("keeps every sample across frames at 48 and 44.1 kHz", () => {
		for (const rate of [48000, 44100]) {
			const resampler = new Pcm16kResampler();
			let out = 0;
			const frames = 200;
			for (let index = 0; index < frames; index++)
				out += resampler.push(new Float32Array(1024).fill(0.5), rate).length;
			const expected = (frames * 1024 * 16000) / rate;
			assert(Math.abs(out - expected) <= 1, `${rate}: ${out} samples, expected ${expected}`);
		}
		// A constant stays constant over frame boundaries (no clicks).
		const resampler = new Pcm16kResampler();
		const samples = [0, 1, 2].flatMap(() => [...resampler.push(new Float32Array(1024).fill(0.25), 44100)]);
		assert(samples.every((sample) => sample === samples[0]));
	});
});

describe("voice answers read while they stream", () => {
	it("reads the text before a tool call without waiting for the call's arguments", () => {
		const view = (content: unknown[]) =>
			({
				entries: [],
				docs: { "pi.live": { run: {}, generation: { message: { role: "assistant", timestamp: 1, content } } } },
			}) as unknown as ConversationView;
		const [streaming] = responses(view([{ type: "text", text: "I will hand this to a doer." }]));
		assert.deepEqual({ done: streaming?.done, closed: streaming?.closed }, { done: false, closed: false });
		const [calling] = responses(
			view([
				{ type: "text", text: "I will hand this to a doer." },
				{ type: "toolCall", id: "1", name: "subagent", arguments: { brief: "a long br" } },
			]),
		);
		assert.deepEqual({ done: calling?.done, closed: calling?.closed }, { done: false, closed: true });
		const chunker = new SpeechChunker();
		assert.deepEqual(chunker.push(streaming?.text ?? "", streaming?.closed), []);
		assert.deepEqual(chunker.push(calling?.text ?? "", calling?.closed), ["I will hand this to a doer."]);
		// Text after the call continues where the chunker stopped.
		assert.deepEqual(chunker.push(`${calling?.text}\nDone. It works.`, true), ["Done.", "It works."]);
	});
});

describe("voice room frames", () => {
	it("splits a binary frame into kind, ID and PCM past the pad byte, copied to a buffer of its own", () => {
		// Kind 1, ID "a-1": 2 + 3 bytes is odd, so one pad byte; the PCM starts at 6.
		const odd = parseRoomFrame(new Uint8Array([1, 3, 0x61, 0x2d, 0x31, 0, 1, 2, 3, 4]));
		assert.deepEqual({ ...odd, pcm: [...(odd?.pcm ?? [])] }, { kind: 1, id: "a-1", pcm: [1, 2, 3, 4] });
		// Kind 2, ID "ab": 2 + 2 bytes is even, no pad.
		const even = parseRoomFrame(new Uint8Array([2, 2, 0x61, 0x62, 5, 6]));
		assert.deepEqual({ ...even, pcm: [...(even?.pcm ?? [])] }, { kind: 2, id: "ab", pcm: [5, 6] });
		assert.equal(odd?.pcm.byteOffset, 0);
		assert.equal(parseRoomFrame(new Uint8Array([1, 9, 1])), undefined);
		assert.equal(roomName("s1", 1), "p2-s1-1");
		assert.equal(roomName("bad id", 1), undefined);
	});
});

describe("voice in chat mode", () => {
	it("turns the agent's name spoken anywhere into a mention in front, keeping what was said", () => {
		for (const [spoken, written] of [
			["Pi, what time is it?", "@pi Pi, what time is it?"],
			["Hey Pi, are you there?", "@pi Hey Pi, are you there?"],
			["can Pi summarize this", "@pi can Pi summarize this"],
			["what do you think, Pi?", "@pi what do you think, Pi?"],
			["  PI.", "@pi PI."],
			["Ask @pi later", "Ask @pi later"],
		])
			assert.equal(spokenMention(spoken), written, spoken);
		// Whole words only; a possessive or contraction ("Pi's") talks about the agent, not to it.
		for (const text of ["Pie is great.", "Piano lessons today", "pip install it", "Pi's answer was odd", "pi-hole"])
			assert.equal(spokenMention(text), text, text);
		// A renamed agent: its name counts, Pi's does not.
		assert.equal(spokenMention("hey Claude, run it", "Claude"), "@claude hey Claude, run it");
		assert.equal(spokenMention("Pi, do it", "Claude"), "Pi, do it");
		assert.equal(spokenMention("Claudette called", "Claude"), "Claudette called");
		assert.deepEqual(voiceMessage("Pi, go", false), { text: "Pi, go", interrupts: true });
		assert.deepEqual(voiceMessage("Pi, go", true), { text: "@pi Pi, go", interrupts: true });
		assert.deepEqual(voiceMessage("lunch?", true), { text: "lunch?", interrupts: false });
		assert.deepEqual(voiceMessage("thanks Claude", true, "Claude"), {
			text: "@claude thanks Claude",
			interrupts: true,
		});
	});

	it("does not stop a busy run for a message that is not for Pi", async () => {
		const calls: string[] = [];
		const queue = new PendingTranscripts({
			target: () => ({
				send: async (text) => calls.push(`send ${text}`),
				stop: async () => {
					calls.push("stop");
					return { restored: [] };
				},
			}),
			busy: () => true,
			log: () => {},
			onError: () => {},
			onChange: () => {},
		});
		queue.push("lunch?", false);
		await queue.flush();
		queue.push("@pi which place?", true);
		await queue.flush();
		assert.deepEqual(calls, ["send lunch?", "stop", "send @pi which place?"]);
	});
});

describe("voice choice against the service", () => {
	it("keeps a language the engine speaks, else falls back to its default; lists English and Deutsch first", () => {
		const supertonic = VOICES.engines[0];
		assert.deepEqual(
			languagesOf(supertonic).map((option) => option.id),
			["en", "de", "fr"],
		);
		const german = { engine: "supertonic", voice: "supertonic-F2", speed: 1, language: "de" };
		assert.deepEqual(resolved(VOICES, german), german);
		// Kokoro speaks English only: switching to it resets the language.
		assert.equal(resolved(VOICES, { ...german, engine: "kokoro", voice: "kokoro-af_heart" }).language, "en");
		// Unknown or absent: the engine's (here the service's) default.
		assert.equal(resolved(VOICES, { ...german, language: "xx" }).language, "en");
		assert.equal(resolved(VOICES, { engine: "", voice: "", speed: 1 }).language, "en");
		// An engine gone: the service's default voice and language.
		assert.deepEqual(resolved(VOICES, { engine: "gone", voice: "gone-1", speed: 1.2, language: "de" }), {
			engine: "supertonic",
			voice: "supertonic-F1",
			speed: 1.2,
			language: "de",
		});
	});
});
