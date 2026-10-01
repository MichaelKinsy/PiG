package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The pi.events bridge fixtures. Every language runs one interpreter over the same configuration, so a scenario states its expectation once and every realm kind must produce it: node is the control that runs upstream's own bus semantics (packages/coding-agent/src/core/event-bus.ts:12-33), a native realm must produce the same lines.
//
// A fixture logs one line per observation to a shared file: `<name>|<channel>|<payload as JSON>`. Its commands are:
//
//	emit <channel> <kind> [json]   emit a payload; kinds: json, undefined, toJSON, getter, bigint, bigintfield, cycle, nan, negzero, function, symbol (all but json and undefined are JavaScript-only payloads)
//	sub <channel> <actions>        subscribe at run time; prints the subscription index as the command's notification
//	unsub <index>                  call a subscription's unsubscribe function
//	burst <channel> <count>        emit {"i":n} count times
//	report                         log "<name>|counts|{channel: deliveries}" for the count action
//	emitmark <channel> <json>      emit, then log "<channel>:returned" (a native emitter)
//
// An action list joins operations with "+": later (node only: record from a microtask, the listener's first continuation), record, echo=<channel> (emit {by, got} from the handler), inc (data.n++), fail (return or throw an error), panic (a Go panic, a thrown non-Error), unsub=<index>, exit (end the process), sleep=<ms>, count (count the delivery), hang (block until <log>.release exists).
type busFixture struct {
	Name      string          `json:"name"`
	Log       string          `json:"log"`
	Listeners []busListenerAt `json:"listeners"`
	// FailLoad makes the factory fail after it subscribed: a node factory throws, a native one registers under a name the Host rejects.
	FailLoad bool `json:"failLoad"`
	// Language, isolation and the Host's working directory are not part of the interpreter's configuration. The directory is where a native fixture finds its configuration file.
	language  string
	isolation string
	cwd       string
}

type busListenerAt struct {
	Channel string `json:"channel"`
	Actions string `json:"actions"`
}

func (f busFixture) configJSON() string {
	if f.Listeners == nil {
		f.Listeners = []busListenerAt{}
	}
	data, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return string(data)
}

const nodeBusFixture = `import fs from "node:fs";
export default function (pi) {
  const cfg = %s;
  const subs = [];
  const counts = {};
  const show = data => { try { return JSON.stringify(data) ?? "null"; } catch { return '"<unserializable>"'; } };
  const log = (channel, data) => fs.appendFileSync(cfg.log, cfg.name + "|" + channel + "|" + show(data) + "\n");
  const run = (channel, actions, data) => {
    for (const op of actions.split("+")) {
      const [name, arg] = op.split("=");
      if (name === "record") log(channel, data);
      else if (name === "later") Promise.resolve().then(() => log(channel + ":later", data));
      else if (name === "echo") pi.events.emit(arg, { by: cfg.name, got: data });
      else if (name === "inc") data.n++;
      else if (name === "fail") throw new Error("listener-failed");
      else if (name === "panic") throw "listener-panicked";
      else if (name === "unsub") subs[Number(arg)]();
      else if (name === "exit") process.exit(3);
      else if (name === "sleep") Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, Number(arg));
      else if (name === "count") counts[channel] = (counts[channel] ?? 0) + 1;
      else if (name === "hang") { while (!fs.existsSync(cfg.log + ".release")) Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 5); }
    }
  };
  const subscribe = (channel, actions) => { subs.push(pi.events.on(channel, data => run(channel, actions, data))); return subs.length - 1; };
  for (const l of cfg.listeners) subscribe(l.channel, l.actions);
  if (cfg.failLoad) throw new Error("factory failed after subscribing");
  const payload = (kind, json) => {
    if (kind === "json") return JSON.parse(json);
    if (kind === "undefined") return undefined;
    if (kind === "toJSON") return { toJSON() { return { via: "toJSON" }; } };
    if (kind === "getter") { globalThis.__reads = (globalThis.__reads ?? 0); return { get n() { return ++globalThis.__reads; } }; }
    if (kind === "bigint") return 1n;
    if (kind === "bigintfield") return { n: 1n };
    if (kind === "nan") return NaN;
    if (kind === "negzero") return -0;
    if (kind === "function") return () => 1;
    if (kind === "symbol") return Symbol("s");
    if (kind === "cycle") { const o = {}; o.self = o; return o; }
    throw new Error("unknown payload kind " + kind);
  };
  pi.registerCommand("emit", { description: "emit", handler: async args => { const [channel, kind, ...rest] = args.split(" "); pi.events.emit(channel, payload(kind, rest.join(" "))); } });
  pi.registerCommand("sub", { description: "sub", handler: async (args, ctx) => { const [channel, actions] = args.split(" "); subscribe(channel, actions); } });
  pi.registerCommand("unsub", { description: "unsub", handler: async args => { subs[Number(args)](); } });
  pi.registerCommand("emitmark", { description: "emitmark", handler: async args => { const [channel, ...rest] = args.split(" "); pi.events.emit(channel, JSON.parse(rest.join(" "))); log(channel + ":returned", null); } });
  pi.registerCommand("report", { description: "report", handler: async () => { log("counts", counts); } });
  pi.registerCommand("burst", { description: "burst", handler: async args => { const [channel, count] = args.split(" "); for (let i = 0; i < Number(count); i++) pi.events.emit(channel, { i }); } });
}
`

const goBusFixture = `package flags

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The configuration is read at start-up from the extension's working directory, which is its Host's own directory, so every rig of the same fixture name runs one compiled program.
var cfgJSON = func() string {
	data, err := os.ReadFile(%s)
	if err != nil {
		panic(err)
	}
	return string(data)
}()

type config struct {
	Name      string
	Log       string
	Listeners []struct{ Channel, Actions string }
	FailLoad  bool
}

func Extension() *sdk.Extension {
	var cfg config
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		panic(err)
	}
	name := cfg.Name
	if cfg.FailLoad {
		name += "-rejected"
	}
	e := sdk.New(name)
	var mu sync.Mutex
	var subs []func()
	counts := map[string]int{}
	logf := func(channel string, data any) {
		encoded, err := json.Marshal(data)
		if err != nil {
			encoded = []byte("\"<unserializable>\"")
		}
		f, err := os.OpenFile(cfg.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		fmt.Fprintf(f, "%%s|%%s|%%s\n", cfg.Name, channel, encoded)
	}
	run := func(ctx sdk.Context, channel, actions string, data any) error {
		for op := range strings.SplitSeq(actions, "+") {
			name, arg, _ := strings.Cut(op, "=")
			switch name {
			case "record":
				logf(channel, data)
			case "echo":
				if err := ctx.Events().Emit(arg, map[string]any{"by": cfg.Name, "got": data}); err != nil {
					return err
				}
			case "inc":
				if m, ok := data.(map[string]any); ok {
					m["n"] = m["n"].(float64) + 1
				}
			case "fail":
				return errors.New("listener-failed")
			case "panic":
				panic("listener-panicked")
			case "unsub":
				index, _ := strconv.Atoi(arg)
				mu.Lock()
				unsubscribe := subs[index]
				mu.Unlock()
				unsubscribe()
			case "exit":
				os.Exit(3)
			case "sleep":
				ms, _ := strconv.Atoi(arg)
				time.Sleep(time.Duration(ms) * time.Millisecond)
			case "count":
				mu.Lock()
				counts[channel]++
				mu.Unlock()
			case "hang":
				for {
					if _, err := os.Stat(cfg.Log + ".release"); err == nil {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
		}
		return nil
	}
	subscribe := func(bus sdk.EventBus, channel, actions string) error {
		unsubscribe, err := bus.On(channel, func(ctx sdk.Context, data any) error { return run(ctx, channel, actions, data) })
		if err != nil {
			return err
		}
		mu.Lock()
		subs = append(subs, unsubscribe)
		mu.Unlock()
		return nil
	}
	for _, l := range cfg.Listeners {
		if err := subscribe(e.Events(), l.Channel, l.Actions); err != nil {
			panic(err)
		}
	}
	e.Command("emit", "", func(ctx sdk.Context, args string) error {
		parts := strings.SplitN(args, " ", 3)
		var payload any
		switch parts[1] {
		case "json":
			if err := json.Unmarshal([]byte(parts[2]), &payload); err != nil {
				return err
			}
		case "undefined":
		default:
			return fmt.Errorf("payload kind %%s is JavaScript-only", parts[1])
		}
		return ctx.Events().Emit(parts[0], payload)
	})
	e.Command("sub", "", func(ctx sdk.Context, args string) error {
		channel, actions, _ := strings.Cut(args, " ")
		return subscribe(ctx.Events(), channel, actions)
	})
	e.Command("unsub", "", func(ctx sdk.Context, args string) error {
		index, _ := strconv.Atoi(args)
		mu.Lock()
		unsubscribe := subs[index]
		mu.Unlock()
		unsubscribe()
		return nil
	})
	e.Command("emitmark", "", func(ctx sdk.Context, args string) error {
		channel, payload, _ := strings.Cut(args, " ")
		var data any
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return err
		}
		if err := ctx.Events().Emit(channel, data); err != nil {
			return err
		}
		logf(channel+":returned", nil)
		return nil
	})
	e.Command("report", "", func(ctx sdk.Context, args string) error {
		mu.Lock()
		snapshot := map[string]int{}
		for channel, n := range counts {
			snapshot[channel] = n
		}
		mu.Unlock()
		logf("counts", snapshot)
		return nil
	})
	e.Command("burst", "", func(ctx sdk.Context, args string) error {
		channel, count, _ := strings.Cut(args, " ")
		n, _ := strconv.Atoi(count)
		for i := range n {
			if err := ctx.Events().Emit(channel, map[string]any{"i": i}); err != nil {
				return err
			}
		}
		return nil
	})
	return e
}
`

const pythonBusFixture = `import json
import os
import threading
import time

import pig_sdk

with open(%s, encoding="utf-8") as config_file:
    CFG = json.load(config_file)


def new_extension():
    name = CFG["name"]
    e = pig_sdk.Extension(name + "-rejected" if CFG.get("failLoad") else name)
    lock = threading.Lock()
    subs = []
    counts = {}

    def logf(channel, data):
        try:
            text = json.dumps(data, separators=(",", ":"), ensure_ascii=False)
        except (TypeError, ValueError):
            text = '"<unserializable>"'
        with open(CFG["log"], "a", encoding="utf-8") as f:
            f.write(f"{name}|{channel}|{text}\n")

    def run(ctx, channel, actions, data):
        for op in actions.split("+"):
            op_name, _, arg = op.partition("=")
            if op_name == "record":
                logf(channel, data)
            elif op_name == "echo":
                ctx.events.emit(arg, {"by": name, "got": data})
            elif op_name == "inc":
                data["n"] = data["n"] + 1
            elif op_name == "fail":
                raise RuntimeError("listener-failed")
            elif op_name == "panic":
                raise ValueError("listener-panicked")
            elif op_name == "unsub":
                with lock:
                    unsubscribe = subs[int(arg)]
                unsubscribe()
            elif op_name == "exit":
                os._exit(3)
            elif op_name == "sleep":
                time.sleep(int(arg) / 1000)
            elif op_name == "count":
                with lock:
                    counts[channel] = counts.get(channel, 0) + 1
            elif op_name == "hang":
                while not os.path.exists(CFG["log"] + ".release"):
                    time.sleep(0.005)

    def subscribe(bus, channel, actions):
        unsubscribe = bus.on(channel, lambda ctx, data: run(ctx, channel, actions, data))
        with lock:
            subs.append(unsubscribe)

    for listener in CFG["listeners"]:
        subscribe(e.events, listener["channel"], listener["actions"])

    def emit(ctx, args):
        channel, kind, *rest = args.split(" ")
        if kind == "json":
            payload = json.loads(" ".join(rest))
        elif kind == "undefined":
            payload = None
        else:
            raise RuntimeError("payload kind " + kind + " is JavaScript-only")
        ctx.events.emit(channel, payload)

    def emitmark(ctx, args):
        channel, _, payload = args.partition(" ")
        ctx.events.emit(channel, json.loads(payload))
        logf(channel + ":returned", None)

    def sub(ctx, args):
        channel, _, actions = args.partition(" ")
        subscribe(ctx.events, channel, actions)

    def unsub(ctx, args):
        with lock:
            unsubscribe = subs[int(args)]
        unsubscribe()

    def burst(ctx, args):
        channel, _, count = args.partition(" ")
        for i in range(int(count)):
            ctx.events.emit(channel, {"i": i})

    def report(ctx, args):
        with lock:
            snapshot = dict(counts)
        logf("counts", snapshot)

    e.command("emit", "", emit)
    e.command("emitmark", "", emitmark)
    e.command("sub", "", sub)
    e.command("unsub", "", unsub)
    e.command("burst", "", burst)
    e.command("report", "", report)
    return e
`

const rustBusFixture = `use pig_sdk::{CommandResult, Context, EventBus, Extension, Subscription};
use serde_json::{json, Value};
use std::collections::BTreeMap;
use std::io::Write;
use std::sync::{Arc, Mutex};

fn cfg_text() -> String {
    std::fs::read_to_string(%s).unwrap()
}

struct Shared {
    cfg: Value,
    subs: Mutex<Vec<Subscription>>,
    counts: Mutex<BTreeMap<String, u64>>,
}

fn log(shared: &Shared, channel: &str, data: &Value) {
    let text = serde_json::to_string(data).unwrap_or_else(|_| "\"<unserializable>\"".to_string());
    let mut f = std::fs::OpenOptions::new().append(true).create(true).open(shared.cfg["log"].as_str().unwrap()).unwrap();
    let line = format!("{}|{}|{}\n", shared.cfg["name"].as_str().unwrap(), channel, text);
    f.write_all(line.as_bytes()).unwrap();
}

fn run(shared: &Arc<Shared>, ctx: &Context, channel: &str, actions: &str, data: &Value) -> Result<(), String> {
    let mut data = data.clone();
    for op in actions.split('+') {
        let (name, arg) = op.split_once('=').unwrap_or((op, ""));
        match name {
            "record" => log(shared, channel, &data),
            "echo" => ctx.events().emit(arg, json!({"by": shared.cfg["name"], "got": data.clone()})).map_err(|e| e.to_string())?,
            "inc" => { let n = data["n"].as_i64().unwrap_or(0); data["n"] = json!(n + 1); }
            "fail" => return Err("listener-failed".to_string()),
            "panic" => panic!("listener-panicked"),
            "unsub" => { let sub = shared.subs.lock().unwrap()[arg.parse::<usize>().unwrap()].clone(); sub.unsubscribe(); }
            "exit" => std::process::exit(3),
            "sleep" => std::thread::sleep(std::time::Duration::from_millis(arg.parse().unwrap())),
            "count" => { *shared.counts.lock().unwrap().entry(channel.to_string()).or_insert(0) += 1; }
            "hang" => { let release = format!("{}.release", shared.cfg["log"].as_str().unwrap()); while !std::path::Path::new(&release).exists() { std::thread::sleep(std::time::Duration::from_millis(5)); } }
            _ => {}
        }
    }
    Ok(())
}

fn subscribe(shared: &Arc<Shared>, bus: &EventBus, channel: &str, actions: &str) -> Result<(), String> {
    let (s, ch, acts) = (shared.clone(), channel.to_string(), actions.to_string());
    let sub = bus.on(channel, move |ctx, data| run(&s, ctx, &ch, &acts, data)).map_err(|e| e.to_string())?;
    shared.subs.lock().unwrap().push(sub);
    Ok(())
}

pub fn new_extension() -> Extension {
    let cfg: Value = serde_json::from_str(&cfg_text()).unwrap();
    let name = cfg["name"].as_str().unwrap().to_string();
    let sdk_name = if cfg["failLoad"] == Value::Bool(true) { format!("{name}-rejected") } else { name };
    let shared = Arc::new(Shared { cfg: cfg.clone(), subs: Mutex::new(Vec::new()), counts: Mutex::new(BTreeMap::new()) });
    let mut e = Extension::new(sdk_name);
    for listener in cfg["listeners"].as_array().unwrap() {
        subscribe(&shared, &e.events(), listener["channel"].as_str().unwrap(), listener["actions"].as_str().unwrap()).unwrap();
    }
    let s = shared.clone();
    e.command("emit", "", move |ctx, args| {
        let _ = &s;
        let mut parts = args.splitn(3, ' ');
        let (channel, kind, rest) = (parts.next().unwrap(), parts.next().unwrap_or(""), parts.next().unwrap_or(""));
        let payload = match kind {
            "json" => serde_json::from_str(rest).unwrap(),
            "undefined" => Value::Null,
            other => return CommandResult::Error(format!("payload kind {other} is JavaScript-only")),
        };
        match ctx.events().emit(channel, payload) { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    let s = shared.clone();
    e.command("emitmark", "", move |ctx, args| {
        let (channel, payload) = args.split_once(' ').unwrap();
        if let Err(err) = ctx.events().emit(channel, serde_json::from_str(payload).unwrap()) { return CommandResult::Error(err.to_string()); }
        log(&s, &format!("{channel}:returned"), &Value::Null);
        CommandResult::Ok
    });
    let s = shared.clone();
    e.command("sub", "", move |ctx, args| {
        let (channel, actions) = args.split_once(' ').unwrap();
        match subscribe(&s, &ctx.events(), channel, actions) { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err) }
    });
    let s = shared.clone();
    e.command("unsub", "", move |_, args| {
        let sub = s.subs.lock().unwrap()[args.trim().parse::<usize>().unwrap()].clone();
        sub.unsubscribe();
        CommandResult::Ok
    });
    e.command("burst", "", move |ctx, args| {
        let (channel, count) = args.split_once(' ').unwrap();
        for i in 0..count.trim().parse::<u64>().unwrap() {
            if let Err(err) = ctx.events().emit(channel, json!({"i": i})) { return CommandResult::Error(err.to_string()); }
        }
        CommandResult::Ok
    });
    let s = shared.clone();
    e.command("report", "", move |_, _| {
        let counts: BTreeMap<String, u64> = s.counts.lock().unwrap().clone();
        log(&s, "counts", &serde_json::to_value(counts).unwrap());
        CommandResult::Ok
    });
    e
}
`

// busConfig writes a fixture of the fixture's language and returns its configuration.
func busConfig(t *testing.T, root string, f busFixture) subprocess.ExtConfig {
	t.Helper()
	isolation := f.isolation
	if isolation == "" {
		isolation = "shared-ok"
	}
	if f.language == "node" {
		path := filepath.Join(t.TempDir(), f.Name+".mjs")
		if err := os.WriteFile(path, []byte(fmt.Sprintf(nodeBusFixture, f.configJSON())), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: f.Name, Source: path, Enabled: true, Isolation: isolation}
	}
	// A native fixture's program does not depend on its configuration: it reads <Name>.bus.json from its working directory. Every rig of one language and name therefore compiles the same source, and the rigs share the build their Hosts cache under busBuildRoot.
	configFile := strconv.Quote(f.Name + ".bus.json")
	if f.cwd == "" {
		t.Fatalf("fixture %s has no working directory", f.Name)
	}
	if err := os.WriteFile(filepath.Join(f.cwd, f.Name+".bus.json"), []byte(f.configJSON()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := busNativeSource(t, root, f.language, f.Name, configFile)
	cfg.Isolation = isolation
	return cfg
}

var (
	busSourcesMu sync.Mutex
	busSources   = map[string]subprocess.ExtConfig{}
)

// busNativeSource returns the fixture's source configuration, writing its program once for the package run into a directory under busBuildRoot. The same path and ContentHash for every rig keep the Host's cell key and cache identity stable, so the cell compiles once.
func busNativeSource(t *testing.T, root, language, name, configFile string) subprocess.ExtConfig {
	t.Helper()
	key := language + "-" + name
	busSourcesMu.Lock()
	defer busSourcesMu.Unlock()
	if cfg, ok := busSources[key]; ok {
		return cfg
	}
	dir := filepath.Join(busBuildRoot, "src", key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := packedFlagFactoryIn(t, dir, root, language, name, false)
	cfg.ContentHash = "events-bridge-" + key
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		code = fmt.Sprintf(goBusFixture, configFile)
	case "python":
		path = filepath.Join(cfg.Source, cfg.Package+".py")
		code = fmt.Sprintf(pythonBusFixture, configFile)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(rustBusFixture, configFile)
		manifest := fmt.Sprintf("[package]\nname=%q\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\nserde_json=\"1\"\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs")))
		if err := os.WriteFile(filepath.Join(cfg.Source, "Cargo.toml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("no event bus fixture for %s", language)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	busSources[key] = cfg
	return cfg
}

// busRig loads the fixtures in order and returns them by name with the shared log.
type busRig struct {
	t    *testing.T
	log  string
	cwd  string
	exts map[string]extension.Extension
	host *subprocess.Host
}

func newBusRig(t *testing.T, specs ...busFixture) *busRig {
	t.Helper()
	rig, failures := newBusRigWithFailures(t, specs...)
	if len(failures) != 0 {
		t.Fatalf("load failures: %v", failures)
	}
	return rig
}

func newBusRigWithFailures(t *testing.T, specs ...busFixture) (*busRig, []error) {
	t.Helper()
	// TestMain exports the three PIG_SDK_*_ROOT variables for this module root; a rig does not set them again, so rigs run in parallel tests.
	root := findModuleRoot(t)
	log := filepath.Join(t.TempDir(), "bus.log")
	cwd := t.TempDir()
	var configs []subprocess.ExtConfig
	for _, spec := range specs {
		spec.Log = log
		spec.cwd = cwd
		configs = append(configs, busConfig(t, root, spec))
	}
	h := subprocess.NewHostWithConfigRoot(cwd, busBuildRoot)
	t.Cleanup(func() { h.Shutdown("test complete") })
	loaded, failures := h.LoadAll(t.Context(), configs)
	if len(failures) == 0 && len(loaded) != len(configs) {
		t.Fatalf("loaded %d of %d extensions without a failure", len(loaded), len(configs))
	}
	rig := &busRig{t: t, log: log, cwd: cwd, host: h, exts: map[string]extension.Extension{}}
	for _, ext := range loaded {
		rig.exts[ext.Name] = ext
	}
	return rig, failures
}

// run executes one command of a fixture, as the user would.
func (r *busRig) run(name, command, args string) error {
	r.t.Helper()
	ext, ok := r.exts[name]
	if !ok {
		r.t.Fatalf("no extension %s", name)
	}
	cmd, ok := ext.Commands[command]
	if !ok {
		r.t.Fatalf("%s has no command %s", name, command)
	}
	return cmd.Handler(r.t.Context(), args)
}

func (r *busRig) must(name, command, args string) {
	r.t.Helper()
	if err := r.run(name, command, args); err != nil {
		r.t.Fatalf("%s %s %s: %v", name, command, args, err)
	}
}

// lines returns the log so far, one entry per observation.
func (r *busRig) lines() []string {
	r.t.Helper()
	data, err := os.ReadFile(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func (r *busRig) reset() {
	r.t.Helper()
	if err := os.Remove(r.log); err != nil && !os.IsNotExist(err) {
		r.t.Fatal(err)
	}
}

func (r *busRig) expect(want ...string) {
	r.t.Helper()
	got := r.lines()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		r.t.Fatalf("log differs\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
