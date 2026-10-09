//! A user_bash handler's `{ operations }` (types.ts UserBashEventResult, core/tools/bash.ts BashOperations) against a fake host: the
//! reply names the object by handle, `user_bash_exec` runs its exec with the command, cwd, timeout and env while every `on_data` chunk
//! travels as a `tool_update` before the answer, and the host's release drops the object.
#![cfg(unix)]

use pig_sdk::{BashOperations, Extension};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
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

struct Host {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<std::io::Result<()>>>,
    root: std::path::PathBuf,
    register: Value,
}

impl Host {
    fn start(ext: Extension) -> Host {
        let root = std::env::temp_dir().join(format!("pig-user-bash-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        stream.set_read_timeout(Some(Duration::from_secs(10))).unwrap();
        let register = read(&mut stream);
        assert_eq!(register["type"], "register");
        send(&mut stream, &json!({"type":"ready","ready":{"state":{},"width":80,"mode":"tui"}}));
        Host { stream, thread: Some(thread), root, register }
    }

    fn next(&mut self) -> Value {
        loop {
            let frame = read(&mut self.stream);
            if frame["type"] != "request_state" {
                return frame;
            }
        }
    }

    fn request(&mut self, id: &str, method: &str, tool: &str, args: Value) {
        send(
            &mut self.stream,
            &json!({"type":"request","id":id,"request":{"method":method,"tool":tool,"tool_call_id":"c","args":args}}),
        );
    }

    fn response(&mut self, id: &str) -> Value {
        let frame = self.next();
        assert_eq!(frame["type"], "response", "frame: {frame}");
        assert_eq!(frame["id"], id);
        frame["response"].clone()
    }

    fn finish(mut self) {
        send(&mut self.stream, &json!({"type":"shutdown","shutdown":{"reason":"done"}}));
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

fn operations() -> (BashOperations, Arc<Mutex<Option<(Option<f64>, Option<std::collections::HashMap<String, String>>)>>>) {
    let seen = Arc::new(Mutex::new(None));
    let recorded = seen.clone();
    let operations = BashOperations {
        exec: Arc::new(move |command, cwd, options| {
            *recorded.lock().unwrap() = Some((options.timeout, options.env.clone()));
            (options.on_data)(b"one");
            (options.on_data)(&[0xff, 0x00]);
            match command {
                "reject" => Err(format!("rejected: {cwd}")),
                "none" => Ok(None),
                _ => Ok(Some(5)),
            }
        }),
    };
    (operations, seen)
}

fn exec(host: &mut Host, id: &str, command: &str) -> (Vec<Value>, Value) {
    host.request(id, "user_bash_exec", "bash-1", json!({"command": command, "cwd": "/w", "timeout": 1.5, "env": {"K": "V"}}));
    let mut updates = Vec::new();
    loop {
        let frame = host.next();
        if frame["type"] == "notify" && frame["notify"]["method"] == "tool_update" {
            updates.push(frame["notify"]["args"].clone());
        } else if frame["type"] == "response" {
            assert_eq!(frame["id"], id);
            return (updates, frame["response"].clone());
        }
    }
}

#[test]
fn operations_stay_in_the_extension_until_the_host_releases_them() {
    let (operations, seen) = operations();
    let mut ext = Extension::new("user-bash");
    let held = Arc::new(Mutex::new(Some(operations)));
    ext.on_event("user_bash", false, move |ctx, _data| held.lock().unwrap().take().map(|operations| ctx.bash_operations(operations)));
    let mut host = Host::start(ext);
    let handler_id = host.register["register"]["handlers"][0]["handler_id"].as_u64().unwrap();
    send(
        &mut host.stream,
        &json!({"type":"request","id":"u1","request":{"method":"event","event":"user_bash","handler_id":handler_id,"args":{"type":"user_bash","command":"x","excludeFromContext":false,"cwd":"/w"}}}),
    );
    let reply = host.response("u1");
    assert_eq!(reply["result"], json!({"operations": {"handle": "bash-1"}}), "{reply}");

    let (updates, answer) = exec(&mut host, "e1", "run");
    assert_eq!(answer["result"], json!({"exitCode": 5}));
    assert_eq!(updates.iter().map(|update| update["result"]["data"].as_str().unwrap().to_string()).collect::<Vec<_>>(), vec!["b25l", "/wA="]);
    assert!(updates.iter().all(|update| update["request_id"] == "e1"));
    let (timeout, env) = seen.lock().unwrap().clone().unwrap();
    assert_eq!((timeout, env.unwrap().get("K").cloned()), (Some(1.5), Some("V".to_string())));
    assert_eq!(exec(&mut host, "e2", "none").1["result"], json!({"exitCode": null}));
    assert_eq!(exec(&mut host, "e3", "reject").1["error"]["message"], "rejected: /w");

    send(&mut host.stream, &json!({"type":"notify","notify":{"method":"bash_operations_release","args":{"handle":"bash-1"}}}));
    assert_eq!(exec(&mut host, "e4", "run").1["error"]["message"], "unknown bash operations: bash-1");
    host.finish();
}
