//! Event stream deltas through the Rust SDK's read loop and event dispatch against a fake host: the handler owns its payload (`&mut Value`), so each frame copies the payload once, and only once.
#![cfg(unix)]

use pig_sdk::Extension;
use serde_json::{Value, json};
use std::alloc::{GlobalAlloc, Layout, System};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

/// Counts the bytes every thread of this test binary allocates.
struct Counting;

static ALLOCATED: AtomicUsize = AtomicUsize::new(0);

unsafe impl GlobalAlloc for Counting {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        ALLOCATED.fetch_add(layout.size(), Ordering::Relaxed);
        unsafe { System.alloc(layout) }
    }
    unsafe fn alloc_zeroed(&self, layout: Layout) -> *mut u8 {
        ALLOCATED.fetch_add(layout.size(), Ordering::Relaxed);
        unsafe { System.alloc_zeroed(layout) }
    }
    unsafe fn realloc(&self, ptr: *mut u8, layout: Layout, new_size: usize) -> *mut u8 {
        ALLOCATED.fetch_add(new_size, Ordering::Relaxed);
        unsafe { System.realloc(ptr, layout, new_size) }
    }
    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        unsafe { System.dealloc(ptr, layout) }
    }
}

#[global_allocator]
static GLOBAL: Counting = Counting;

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

/// The response to request `id`, skipping request_state frames.
fn response(stream: &mut UnixStream, id: &str) -> Value {
    loop {
        let frame = read(stream);
        if frame["type"] == "request_state" {
            continue;
        }
        assert_eq!(frame["type"], "response", "frame: {frame}");
        assert_eq!(frame["id"], id, "frame: {frame}");
        return frame;
    }
}

#[test]
fn a_delta_frame_copies_the_payload_once() {
    const BASE: usize = 64 * 1024;
    const FRAMES: usize = 200;
    const CHUNK: &str = "abcdefgh";
    static DELIVERED: AtomicUsize = AtomicUsize::new(0);
    static CALLS: AtomicUsize = AtomicUsize::new(0);
    static LAST: AtomicUsize = AtomicUsize::new(0);
    let mut ext = Extension::new("rs-event-stream-copies");
    let _keep = ext.on_event("message_update", |_, data| {
        let length = data["text"].as_str().map_or(0, str::len);
        DELIVERED.fetch_add(length, Ordering::Relaxed);
        CALLS.fetch_add(1, Ordering::Relaxed);
        LAST.store(length, Ordering::Relaxed);
        None
    });
    let root = std::env::temp_dir().join(format!("pig-event-stream-{}", std::process::id()));
    std::fs::create_dir_all(&root).unwrap();
    let socket = root.join("sock");
    let _ = std::fs::remove_file(&socket);
    let listener = UnixListener::bind(&socket).unwrap();
    let path = socket.to_str().unwrap().to_string();
    let thread = std::thread::spawn(move || ext.run_with_socket(&path));
    let (mut stream, _) = listener.accept().unwrap();
    stream.set_read_timeout(Some(Duration::from_secs(10))).unwrap();
    let register = read(&mut stream);
    assert_eq!(register["type"], "register");
    let handler_id = register["register"]["handlers"][0]["handler_id"].clone();
    send(&mut stream, &json!({"type":"ready","ready":{"state":{},"width":80,"mode":"tui"}}));

    let keyframe = json!({"type":"message_update","text":"x".repeat(BASE),"nested":{"list":[1, "two"]}});
    send(&mut stream, &json!({"type":"request","id":"k","request":{"method":"event","event":"message_update","handler_id":handler_id,"args":keyframe,"stream":{"seq":1}}}));
    response(&mut stream, "k");
    let frames: Vec<(String, Value)> = (0..FRAMES)
        .map(|index| {
            let id = format!("d{index}");
            let seq = index as u64 + 2;
            let ops = json!([{"op":"append","path":["text"],"value":CHUNK}]);
            let frame = json!({"type":"request","id":id,"request":{"method":"event","event":"message_update","handler_id":handler_id,"stream":{"seq":seq,"base":seq - 1,"ops":ops}}});
            (id, frame)
        })
        .collect();
    let before = ALLOCATED.load(Ordering::Relaxed);
    for (id, frame) in &frames {
        send(&mut stream, frame);
        response(&mut stream, id);
    }
    let allocated = ALLOCATED.load(Ordering::Relaxed) - before;
    send(&mut stream, &json!({"type":"shutdown","shutdown":{"reason":"done"}}));
    thread.join().unwrap().unwrap();
    let _ = std::fs::remove_dir_all(&root);

    assert_eq!(CALLS.load(Ordering::Relaxed), FRAMES + 1);
    assert_eq!(LAST.load(Ordering::Relaxed), BASE + FRAMES * CHUNK.len());
    let delivered = DELIVERED.load(Ordering::Relaxed) - BASE;
    let copies = allocated as f64 / delivered as f64;
    eprintln!("bytes allocated per delta frame {}, payload copies per frame {copies:.2}", allocated / FRAMES);
    assert!(copies < 1.3, "each delta frame allocated {copies:.2} payloads ({allocated} bytes for {delivered} payload bytes), want one copy");
}
