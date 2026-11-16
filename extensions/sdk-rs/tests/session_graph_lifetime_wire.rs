//! A session graph object stays alive on the host while a later call still names its handle (Rust SDK, against a fake host).
//!
//! The host frees an object when the extension's `Drop` tells it to, and handles travel on the wire as strings: a value that
//! holds only the string does not keep the object, so the object was released, and the next call that named the string got
//! "Unknown or released session graph object" from the host. The host here records the order in which it sees the calls, with
//! no wait: a release that arrives before the call that names the handle is the failure.
#![cfg(unix)]

use pig_sdk::{
    CommandResult, Context, DefaultResourceLoader, DefaultResourceLoaderOptions, Extension,
    ExtensionRunner, LoadExtensionsResult, ModelRuntime, ModelRuntimeCreateOptions,
};
use serde_json::{Value, json};
use std::io::{self, Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

static NEXT: AtomicUsize = AtomicUsize::new(0);

fn read(stream: &mut UnixStream) -> Value {
    let mut prefix = [0; 4];
    stream.read_exact(&mut prefix).expect("frame prefix");
    let mut data = vec![0; u32::from_be_bytes(prefix) as usize];
    stream.read_exact(&mut data).expect("frame body");
    serde_json::from_slice(&data).unwrap()
}

fn send(stream: &mut UnixStream, frame: &Value) {
    let raw = frame.to_string();
    stream.write_all(&(raw.len() as u32).to_be_bytes()).unwrap();
    stream.write_all(raw.as_bytes()).unwrap();
}

/// A fake host: the accepted socket of one running extension.
struct Host {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<io::Result<()>>>,
    root: std::path::PathBuf,
}

impl Host {
    fn start(ext: Extension) -> Host {
        let root = std::env::temp_dir().join(format!(
            "pig-graphlife-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(10)))
            .unwrap();
        let register = read(&mut stream);
        assert_eq!(register["type"], "register");
        send(
            &mut stream,
            &json!({"type":"ready","ready":{"state":{},"width":80,"mode":"tui"}}),
        );
        Host {
            stream,
            thread: Some(thread),
            root,
        }
    }

    fn next(&mut self) -> Value {
        loop {
            let frame = read(&mut self.stream);
            if frame["type"] != "request_state" {
                return frame;
            }
        }
    }

    fn reply(&mut self, call: &Value, result: Value) {
        send(
            &mut self.stream,
            &json!({"type":"call_result","id":call["id"],"call_result":{"result":result}}),
        );
    }

    fn command(&mut self, id: &str, name: &str) {
        send(
            &mut self.stream,
            &json!({"type":"request","id":id,"request":{"method":"command","tool":name,"args":""}}),
        );
    }

    fn finish(mut self) {
        send(
            &mut self.stream,
            &json!({"type":"shutdown","shutdown":{"reason":"done"}}),
        );
        self.thread.take().unwrap().join().unwrap().unwrap();
    }
}

impl Drop for Host {
    fn drop(&mut self) {
        let _ = self.stream.shutdown(std::net::Shutdown::Both);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
        let _ = std::fs::remove_dir_all(&self.root);
    }
}

/// What the host has seen of the session graph, in the order it saw it.
#[derive(Default)]
struct Graph {
    /// The `sessionGraph.new` calls: kind and arguments. The n-th (from 1) was answered with handle `h<n>`.
    created: Vec<Value>,
    released: Vec<String>,
    /// The `modelStream` calls, which the host holds unanswered until the test answers them.
    streams: Vec<Value>,
    response: Option<Value>,
    /// The n-th (from 1) `sessionGraph.new` the host keeps unanswered, in `held`, until `answer_held`; 0 holds none.
    hold_new: usize,
    held: Option<Value>,
}

impl Graph {
    fn released(&self, handle: &str) -> bool {
        self.released.iter().any(|released| released == handle)
    }

    /// Answers the `sessionGraph.new` the host held.
    fn answer_held(&mut self, host: &mut Host) {
        let frame = self.held.take().expect("no sessionGraph.new is held");
        host.reply(
            &frame,
            json!({"handle": format!("h{}", self.created.len())}),
        );
    }

    /// Reads one frame and answers it as a host does; the `response` of the command is kept.
    fn serve(&mut self, host: &mut Host) {
        let frame = host.next();
        if frame["type"] == "response" {
            self.response = Some(frame["response"].clone());
            return;
        }
        assert_eq!(frame["type"], "call", "frame: {frame}");
        let args = frame["call"]["args"].clone();
        match frame["call"]["method"].as_str().unwrap() {
            "sessionGraph.new" => {
                self.created.push(args);
                if self.hold_new == self.created.len() {
                    self.held = Some(frame);
                    return;
                }
                host.reply(
                    &frame,
                    json!({"handle": format!("h{}", self.created.len())}),
                );
            }
            "sessionGraph.release" => {
                self.released
                    .push(args["handle"].as_str().unwrap().to_string());
                host.reply(&frame, json!({}));
            }
            "sessionGraph.call" if args["method"] == "getExtensions" => host.reply(
                &frame,
                json!({"extensions":[{"path":"/ext/a.js","resolvedPath":"/ext/a.js"}]}),
            ),
            "sessionGraph.call" => host.reply(&frame, Value::Null),
            "modelStream" => self.streams.push(frame),
            other => panic!("unexpected call {other}: {frame}"),
        }
    }

    fn serve_until(&mut self, host: &mut Host, done: impl Fn(&Graph) -> bool) {
        while !done(self) {
            self.serve(host);
        }
    }
}

/// The loader's scope ends here, as a program's does when it keeps only what `get_extensions` returned.
fn load(ctx: &Context) -> io::Result<LoadExtensionsResult> {
    let loader = DefaultResourceLoader::new(ctx, DefaultResourceLoaderOptions::default())?;
    loader.get_extensions()
}

fn name_the_loader(ctx: &Context) -> io::Result<()> {
    let loaded = load(ctx)?;
    // A round trip with the host between the loader's drop and the runner: whatever the drop started has had its turn.
    drop(ModelRuntime::create(
        ctx,
        ModelRuntimeCreateOptions::default(),
    )?);
    let runner = ExtensionRunner::new(
        ctx,
        &loaded.extensions,
        Some(&loaded.runtime),
        "/w",
        None,
        None,
    )?;
    drop(runner);
    Ok(())
}

fn result(outcome: io::Result<()>) -> CommandResult {
    match outcome {
        Ok(()) => CommandResult::Ok,
        Err(err) => CommandResult::Error(err.to_string()),
    }
}

// h1 loader, h2 its runtime (`ExtensionRuntime.of`), h3 the control `ModelRuntime`, h4 the runner.
#[test]
fn a_loaded_extension_keeps_its_loader_until_it_is_dropped() {
    let mut ext = Extension::new("rs-graph-loader");
    ext.command("load", "Load", |ctx, _| result(name_the_loader(ctx)));
    let mut host = Host::start(ext);
    host.command("c1", "load");
    // The host holds the runner's constructor (h4) unanswered until the retention asserts are done: the handler is blocked in it, so the
    // extensions it loaded stay held, whatever order the releases of dropped objects (each sent from its own thread) reach the host in.
    let mut graph = Graph {
        hold_new: 4,
        ..Graph::default()
    };
    graph.serve_until(&mut host, |graph| {
        graph.released("h3") && graph.held.is_some()
    });
    assert!(
        !graph.released("h1"),
        "the loader (h1) was released while the extensions it loaded were still held: released {:?}",
        graph.released
    );
    let wire = &graph.created[3]["args"][0];
    assert_eq!(graph.created[3]["kind"], "ExtensionRunner");
    assert_eq!(wire["extensions"][0]["loader"], "h1", "{wire}");
    assert_eq!(wire["runtime"], "h2", "{wire}");
    assert!(
        !graph.released("h1") && !graph.released("h2"),
        "the runner was made after its loader or runtime was released: released {:?}",
        graph.released
    );
    graph.answer_held(&mut host);
    graph.serve_until(&mut host, |graph| graph.response.is_some());
    assert_eq!(graph.response, Some(json!({})), "{:?}", graph.response);
    // Dropping what held them releases them: the host learns it (the read has the harness's timeout).
    graph.serve_until(&mut host, |graph| {
        ["h1", "h2", "h4"]
            .iter()
            .all(|handle| graph.released(handle))
    });
    host.finish();
}

fn stream_after_drop(ctx: &Context) -> io::Result<()> {
    let runtime = ModelRuntime::create(ctx, ModelRuntimeCreateOptions::default())?;
    let _stream = runtime.stream(
        json!({"id":"m","provider":"p"}),
        json!({"messages":[]}),
        json!({}),
    );
    drop(runtime);
    drop(ModelRuntime::create(
        ctx,
        ModelRuntimeCreateOptions::default(),
    )?);
    Ok(())
}

// h1 the runtime the stream runs on, h2 the control.
#[test]
fn a_stream_keeps_its_runtime_until_the_host_has_answered_it() {
    let mut ext = Extension::new("rs-graph-stream");
    ext.command("stream", "Stream", |ctx, _| result(stream_after_drop(ctx)));
    let mut host = Host::start(ext);
    host.command("c1", "stream");
    let mut graph = Graph::default();
    graph.serve_until(&mut host, |graph| {
        graph.released("h2") && !graph.streams.is_empty()
    });
    assert_eq!(graph.streams[0]["call"]["args"]["runtime"], "h1");
    assert!(
        !graph.released("h1"),
        "the runtime (h1) was released while its stream, which names it, was unanswered: released {:?}",
        graph.released
    );
    let held = graph.streams[0].clone();
    host.reply(&held, json!({}));
    graph.serve_until(&mut host, |graph| {
        graph.response.is_some() && graph.released("h1")
    });
    host.finish();
}
