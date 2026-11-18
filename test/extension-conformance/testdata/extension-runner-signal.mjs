// The Node fixture of the extension_runner_signal row (extension_runner_signal_test.go): Pi's own ExtensionRunner over the signal probe, with a `getSignal` that answers the signal of the client's current run, as AgentSession does with `() => this.agent.signal` (agent-session.ts:3596). Every run of Pi has its own AbortController (agent.ts:518-527); a run that ends live is never aborted afterwards (finishRun, agent.ts:562-566), and a run that follows has a new signal. Its report is the expectation; testfixture/sessiongraph (Go), testdata/session-graph-rust, the inline Python fixture and testdata/session-graph-lisp run the same sequences through their SDK's runner and signals.
import { DefaultResourceLoader, ExtensionRunner, ModelRegistry, ModelRuntime, SessionManager, SettingsManager } from "@earendil-works/pi-coding-agent";

// runnerFor is a runner over the probe, with appendEntry logged and getSignal answering what `current()` returns.
async function runnerFor({ cwd, agentDir, signalProbe, mrAuth, mrModels, mrStore }, current) {
  const rt = await ModelRuntime.create({ authPath: mrAuth, modelsPath: mrModels, modelsStorePath: mrStore });
  const registry = new ModelRegistry(rt);
  const settings = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, defaultProvider: "mr-local", defaultModel: "m1" });
  const loader = new DefaultResourceLoader({
    cwd, agentDir, settingsManager: settings, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true,
    additionalExtensionPaths: [signalProbe],
  });
  await loader.reload();
  const loaded = loader.getExtensions();
  const runner = new ExtensionRunner(loaded.extensions, loaded.runtime, cwd, SessionManager.inMemory(cwd), registry);
  const entries = [];
  const errors = [];
  runner.bindCore({ appendEntry: (customType, data) => entries.push([customType, data]) }, { getSignal: () => current() });
  runner.onError((report) => errors.push({ event: report.event, error: report.error }));
  return { runner, entries, errors, loadErrors: loaded.errors };
}

const turn = { type: "turn_start", turnIndex: 0, timestamp: 1 };
const message = { type: "message_start", message: { role: "user", content: "hi", timestamp: 1 } };

export async function extensionRunnerSignalScript(args) {
  const r = {};

  // Sequence 1, two live runs: run 1 ends live and run 2 begins with no request between them; the abort of run 2 reaches run 2's signal only.
  let cur = new AbortController();
  let rig = await runnerFor(args, () => cur.signal);
  await rig.runner.emit({ type: "agent_start" });
  const second = new AbortController();
  cur = second;
  await rig.runner.emit(turn);
  second.abort();
  await rig.runner.emit(message);
  r.twoLiveRuns = { entries: rig.entries, errors: rig.errors, loadErrors: rig.loadErrors };

  // Sequence 2, the own abort missed: run 1 is aborted and run 2 begins with no request between them; the signal the handler kept is aborted and the current one is not.
  const first = new AbortController();
  cur = first;
  rig = await runnerFor(args, () => cur.signal);
  await rig.runner.emit({ type: "agent_start" });
  first.abort();
  const next = new AbortController();
  cur = next;
  // No step drops `first`: this is Pi's own runner, which keeps nothing of the signals (no PiG wrapper holds one); the other cells drop their own reference to run 1's signal here.
  await rig.runner.emit(turn);
  next.abort();
  await rig.runner.emit(message);
  r.abortedThenLive = { entries: rig.entries, errors: rig.errors, loadErrors: rig.loadErrors };
  return r;
}
