use super::*;

// A frame is one write: a header and a body written apart cost the host two reads.
struct RecordingWriter {
    writes: Vec<Vec<u8>>,
}

impl Write for RecordingWriter {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.writes.push(buf.to_vec());
        Ok(buf.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[test]
fn a_frame_is_one_write() {
    let mut writer = RecordingWriter { writes: Vec::new() };
    let env = serde_json::json!({"type": "pong", "pong": {"nonce": "n1"}});
    Connection::write_frame_to(&mut writer, &env).unwrap();
    assert_eq!(writer.writes.len(), 1, "the header and the body were written apart");
    let frame = &writer.writes[0];
    let length = u32::from_be_bytes(frame[..4].try_into().unwrap()) as usize;
    assert_eq!(length, frame.len() - 4);
    let body: Value = serde_json::from_slice(&frame[4..]).unwrap();
    assert_eq!(body, env);
}

#[test]
fn an_oversized_frame_is_refused_before_any_write() {
    let mut writer = RecordingWriter { writes: Vec::new() };
    let env = serde_json::json!({"nonce": "x".repeat(MAX_FRAME_SIZE as usize + 1)});
    assert!(Connection::write_frame_to(&mut writer, &env).is_err());
    assert!(writer.writes.is_empty());
}

// A response is encoded before the writer lock is taken: encoding hundreds of MiB takes seconds, and the lock also carries the heartbeat's pong.
#[cfg(unix)]
#[test]
fn respond_encodes_its_result_outside_the_writer_lock() {
    use std::sync::mpsc;
    struct Slow {
        entered: mpsc::Sender<()>,
        release: std::sync::Mutex<mpsc::Receiver<()>>,
    }
    impl Serialize for Slow {
        fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
            self.entered.send(()).unwrap();
            self.release.lock().unwrap().recv().unwrap();
            serializer.serialize_bool(true)
        }
    }
    let (ours, theirs) = std::os::unix::net::UnixStream::pair().unwrap();
    let conn = Arc::new(Connection::new(ours));
    let (entered_tx, entered_rx) = mpsc::channel();
    let (release_tx, release_rx) = mpsc::channel();
    let responder = {
        let conn = conn.clone();
        std::thread::spawn(move || conn.respond_serialized("r1", Some(Slow { entered: entered_tx, release: std::sync::Mutex::new(release_rx) }), None))
    };
    entered_rx.recv_timeout(std::time::Duration::from_secs(5)).expect("the result was never encoded");
    let free = conn.writer.try_lock().is_ok();
    release_tx.send(()).unwrap();
    responder.join().unwrap().unwrap();
    assert!(free, "the writer lock was held while the result was being encoded");
    drop(theirs);
}
