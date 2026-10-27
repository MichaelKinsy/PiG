//! Host calls for Pi's pure helpers that need the host's parsers: `parseFrontmatter` (the host's YAML parser, the one that reads skills and prompt templates)
//! and `Date.parse` (the host's port of V8's). They read no session state, so the host serves them before an extension registers, and a helper called while
//! the factory runs (before [`crate::Extension::run`]) dials the host itself: the first call opens the connection and `run` later reuses it. A call from a
//! handler uses the handler's own connection ([`crate::Context::parse_frontmatter`], [`crate::Context::parse_date`]).

use std::io;
use std::sync::{Arc, Mutex, Weak};

use serde_json::{Value, json};

use crate::protocol::Connection;
use crate::transport::UnixStream;

const CALL_PARSE_FRONTMATTER: &str = "frontmatter.parse";
const CALL_PARSE_DATE: &str = "date.parse";
// The host's ports of Pi's generateDiffString, generateUnifiedPatch, copyToClipboard, convertToPng and resizeImage.
const CALL_GENERATE_DIFF_STRING: &str = "diff.string";
const CALL_GENERATE_UNIFIED_PATCH: &str = "diff.patch";
const CALL_COPY_TO_CLIPBOARD: &str = "clipboard.copy";
const CALL_CONVERT_TO_PNG: &str = "image.convertToPng";
const CALL_RESIZE_IMAGE: &str = "image.resize";
/// `highlightCode` and the theme `initTheme` installs: the host's highlight.js port and its theme registry. Like the two above they read no session state, so
/// the host serves them before an extension registers.
const CALL_HIGHLIGHT_CODE: &str = "highlight.code";
const CALL_LOAD_THEME: &str = "theme.load";

/// Pi's `{ frontmatter, body }`. `frontmatter` is `parse(yaml) ?? {}`: an object for the usual mapping header, whose keys follow the order a JavaScript object
/// enumerates them (array-index keys first, ascending, then insertion order), and the array, string, number or boolean of any other YAML document.
#[derive(Debug, Clone, PartialEq)]
pub struct ParsedFrontmatter {
    pub frontmatter: Value,
    pub body: String,
}

/// The connections helpers opened before `run`, by socket path; `run` takes its own.
static DIALLED: Mutex<Vec<(String, Arc<Connection>)>> = Mutex::new(Vec::new());
/// The connections of the extensions that are running (a packed cell runs several), with the socket path each serves: a helper that has no `Context` and no
/// socket of its own uses the first that is still running.
static RUNNING: Mutex<Vec<(String, Weak<Connection>)>> = Mutex::new(Vec::new());

fn locked<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// Pi's `parseFrontmatter` (`utils/frontmatter.ts`, exported from `index.ts`), answered by the host's parser. A header the YAML parser refuses is the error Pi
/// throws. It may be called before `run`: the first call connects to the host, and `run` reuses the connection.
pub fn parse_frontmatter(content: &str) -> io::Result<ParsedFrontmatter> {
    decode_frontmatter(helper_call(CALL_PARSE_FRONTMATTER, json!({"content": content}))?)
}

/// Pi's `stripFrontmatter`: the body of [`parse_frontmatter`].
pub fn strip_frontmatter(content: &str) -> io::Result<String> {
    Ok(parse_frontmatter(content)?.body)
}

/// `Date.parse(text)` (`new Date(text).getTime()` for a string), V8's parser answered by the host: the time value in milliseconds, `None` when it is NaN. Text in the
/// form `toISOString` writes for a four-digit year (`2026-01-02T03:04:05.678Z`) is answered without the host. It may be called before `run`, like [`parse_frontmatter`].
pub fn parse_date(text: &str) -> io::Result<Option<f64>> {
    if let Some(ms) = parse_iso_date(text) {
        return Ok(Some(ms));
    }
    decode_date(helper_call(CALL_PARSE_DATE, json!({"text": text}))?)
}

/// `Date.parse` for the text `Date.prototype.toISOString` writes for a year of four digits, `YYYY-MM-DDTHH:mm:ss.sssZ` with every field in range, which is how Pi
/// writes the timestamp of each session entry. ECMA-262 21.4.1.32 defines that form, so the answer is the same in every engine and needs no host. Text in any other
/// form (a missing Z, an offset, a year of six digits, a lowercase t or z, a field out of range such as February 30 or hour 24) is not decided here: the host's parser
/// answers it, as V8 does.
pub(crate) fn parse_iso_date(text: &str) -> Option<f64> {
    let bytes = text.as_bytes();
    if bytes.len() != 24 || bytes[4] != b'-' || bytes[7] != b'-' || bytes[10] != b'T' || bytes[13] != b':' || bytes[16] != b':' || bytes[19] != b'.' || bytes[23] != b'Z' {
        return None;
    }
    let digits = |from: usize, to: usize| -> Option<i64> {
        let mut n = 0;
        for &byte in &bytes[from..to] {
            if !byte.is_ascii_digit() {
                return None;
            }
            n = n * 10 + i64::from(byte - b'0');
        }
        Some(n)
    };
    let (year, month, day) = (digits(0, 4)?, digits(5, 7)?, digits(8, 10)?);
    let (hour, minute, second, millisecond) = (digits(11, 13)?, digits(14, 16)?, digits(17, 19)?, digits(20, 23)?);
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    let days_in_month = match month {
        2 if leap => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    };
    if !(1..=12).contains(&month) || day < 1 || day > days_in_month || hour > 23 || minute > 59 || second > 59 {
        return None;
    }
    // The days from 1970-01-01 in the proleptic Gregorian calendar, which is what ECMA-262 time values count.
    let shifted = if month <= 2 { year - 1 } else { year };
    let era = shifted.div_euclid(400);
    let year_of_era = shifted - era * 400;
    let day_of_year = (153 * ((month + 9) % 12) + 2) / 5 + day - 1;
    let days = era * 146097 + year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year - 719468;
    Some((((days * 24 + hour) * 60 + minute) * 60000 + second * 1000 + millisecond) as f64)
}

/// The connection of the running extension a call made outside a handler goes through.
pub(crate) fn running_connection() -> io::Result<Arc<Connection>> {
    running_for(&locked(&RUNNING), socket_path().as_deref())
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotConnected, "the extension is not running: a built-in tool without a context can execute only after run has started"))
}

/// The lines the host's highlight.js port makes of `code` in `lang`, drawn with `theme` (the palette of the theme `init_theme` installed) or, when `None`, with the host's
/// active theme.
pub(crate) fn highlight_code_lines(code: &str, lang: &str, markdown: bool, theme: Option<Value>) -> io::Result<Vec<String>> {
    let mut args = json!({"code": code, "lang": lang});
    if markdown {
        args["markdown"] = Value::Bool(true);
    }
    if let Some(theme) = theme {
        args["theme"] = theme;
    }
    let result = helper_call(CALL_HIGHLIGHT_CODE, args)?.unwrap_or(Value::Null);
    match result.get("lines").and_then(Value::as_array) {
        Some(lines) => Ok(lines.iter().filter_map(|line| line.as_str().map(str::to_string)).collect()),
        None => Err(io::Error::new(io::ErrorKind::InvalidData, "decode highlightCode: the host answered no lines")),
    }
}

/// The palette of the theme `initTheme(name)` installs.
pub(crate) fn load_theme(name: Option<&str>) -> io::Result<Value> {
    let result = helper_call(CALL_LOAD_THEME, json!({"name": name}))?.unwrap_or(Value::Null);
    match result.get("theme") {
        Some(theme) if theme.is_object() => Ok(theme.clone()),
        _ => Err(io::Error::new(io::ErrorKind::InvalidData, "initTheme: the host answered a theme that is not a palette")),
    }
}

pub(crate) fn frontmatter_args(content: &str) -> (&'static str, Value) {
    (CALL_PARSE_FRONTMATTER, json!({"content": content}))
}

pub(crate) fn date_args(text: &str) -> (&'static str, Value) {
    (CALL_PARSE_DATE, json!({"text": text}))
}

pub(crate) fn decode_frontmatter(result: Option<Value>) -> io::Result<ParsedFrontmatter> {
    let result = result.unwrap_or(Value::Null);
    Ok(ParsedFrontmatter { frontmatter: result.get("frontmatter").cloned().unwrap_or(Value::Null), body: result.get("body").and_then(Value::as_str).unwrap_or("").to_string() })
}

pub(crate) fn decode_date(result: Option<Value>) -> io::Result<Option<f64>> {
    Ok(result.and_then(|result| result.get("ms").and_then(Value::as_f64)))
}

/// The result of [`generate_diff_string`]: Pi's `{ diff, firstChangedLine }`. `first_changed_line` is `None` when the texts do not differ (undefined in Pi).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffString {
    pub diff: String,
    pub first_changed_line: Option<i64>,
}

/// The result of [`convert_to_png`]: Pi's `{ data, mimeType }`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConvertedImage {
    pub data: String,
    pub mime_type: String,
}

/// Pi's `ImageResizeOptions` (`utils/image-resize-core.ts`). `None` takes Pi's default.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct ImageResizeOptions {
    pub max_width: Option<u32>,
    pub max_height: Option<u32>,
    pub max_bytes: Option<u64>,
    pub jpeg_quality: Option<u32>,
}

fn diff_args(old_content: &str, new_content: &str, context_lines: Option<i64>) -> Value {
    let mut args = json!({"oldContent": old_content, "newContent": new_content});
    if let Some(lines) = context_lines {
        args["contextLines"] = json!(lines);
    }
    args
}

fn patch_args(path: &str, old_content: &str, new_content: &str, context_lines: Option<i64>) -> Value {
    let mut args = diff_args(old_content, new_content, context_lines);
    args["path"] = json!(path);
    args
}

fn resize_args(input: &[u8], mime_type: &str, options: Option<&ImageResizeOptions>) -> Value {
    let mut args = json!({"data": base64_encode(input), "mimeType": mime_type});
    if let Some(options) = options {
        let mut object = serde_json::Map::new();
        if let Some(width) = options.max_width {
            object.insert("maxWidth".into(), json!(width));
        }
        if let Some(height) = options.max_height {
            object.insert("maxHeight".into(), json!(height));
        }
        if let Some(bytes) = options.max_bytes {
            object.insert("maxBytes".into(), json!(bytes));
        }
        if let Some(quality) = options.jpeg_quality {
            object.insert("jpegQuality".into(), json!(quality));
        }
        args["options"] = Value::Object(object);
    }
    args
}

fn base64_encode(bytes: &[u8]) -> String {
    const ALPHABET: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(bytes.len().div_ceil(3) * 4);
    for chunk in bytes.chunks(3) {
        let n = (u32::from(chunk[0]) << 16) | (u32::from(*chunk.get(1).unwrap_or(&0)) << 8) | u32::from(*chunk.get(2).unwrap_or(&0));
        out.push(ALPHABET[(n >> 18) as usize & 63] as char);
        out.push(ALPHABET[(n >> 12) as usize & 63] as char);
        out.push(if chunk.len() > 1 { ALPHABET[(n >> 6) as usize & 63] as char } else { '=' });
        out.push(if chunk.len() > 2 { ALPHABET[n as usize & 63] as char } else { '=' });
    }
    out
}

pub(crate) fn decode_diff_string(result: Option<Value>) -> io::Result<DiffString> {
    let result = result.unwrap_or(Value::Null);
    Ok(DiffString { diff: result.get("diff").and_then(Value::as_str).unwrap_or("").to_string(), first_changed_line: result.get("firstChangedLine").and_then(Value::as_i64) })
}

pub(crate) fn decode_patch(result: Option<Value>) -> io::Result<String> {
    Ok(result.and_then(|result| result.get("patch").and_then(Value::as_str).map(String::from)).unwrap_or_default())
}

pub(crate) fn decode_converted(result: Option<Value>) -> io::Result<Option<ConvertedImage>> {
    let result = result.unwrap_or(Value::Null);
    Ok(result.get("image").filter(|image| image.is_object()).map(|image| ConvertedImage {
        data: image.get("data").and_then(Value::as_str).unwrap_or("").to_string(),
        mime_type: image.get("mimeType").and_then(Value::as_str).unwrap_or("").to_string(),
    }))
}

pub(crate) fn decode_resized(result: Option<Value>) -> io::Result<Option<crate::ResizedImage>> {
    let result = result.unwrap_or(Value::Null);
    let number = |image: &Value, key: &str| image.get(key).and_then(Value::as_f64).unwrap_or(f64::NAN);
    Ok(result.get("image").filter(|image| image.is_object()).map(|image| crate::ResizedImage {
        data: image.get("data").and_then(Value::as_str).unwrap_or("").to_string(),
        mime_type: image.get("mimeType").and_then(Value::as_str).unwrap_or("").to_string(),
        original_width: number(image, "originalWidth"),
        original_height: number(image, "originalHeight"),
        width: number(image, "width"),
        height: number(image, "height"),
        was_resized: image.get("wasResized").and_then(Value::as_bool).unwrap_or(false),
    }))
}

/// Pi's `generateDiffString` (`core/tools/edit-diff.ts`, exported from `index.ts`): the line-numbered display diff of two texts, with the context around each change cut to
/// `context_lines` (default 4) lines, and the first changed line of the new text. The host's byte-exact port answers. It may be called before `run`, like [`parse_frontmatter`].
pub fn generate_diff_string(old_content: &str, new_content: &str, context_lines: Option<i64>) -> io::Result<DiffString> {
    decode_diff_string(helper_call(CALL_GENERATE_DIFF_STRING, diff_args(old_content, new_content, context_lines))?)
}

/// Pi's `generateUnifiedPatch` (`core/tools/edit-diff.ts`, exported from `index.ts`): a standard unified patch of two texts for one path, with `context_lines` (default 4)
/// lines of context around each change. The host's port of jsdiff answers. It may be called before `run`.
pub fn generate_unified_patch(path: &str, old_content: &str, new_content: &str, context_lines: Option<i64>) -> io::Result<String> {
    decode_patch(helper_call(CALL_GENERATE_UNIFIED_PATCH, patch_args(path, old_content, new_content, context_lines))?)
}

/// Pi's `copyToClipboard` (`utils/clipboard.ts`, exported from `index.ts`): the host copies `text` with the strategy its own copy commands use, and a failed copy is the
/// error. It may be called before `run`.
pub fn copy_to_clipboard(text: &str) -> io::Result<()> {
    helper_call(CALL_COPY_TO_CLIPBOARD, json!({"text": text})).map(|_| ())
}

/// Pi's `convertToPng` (`utils/image-convert.ts`, exported from `index.ts`): base64 image data as base64 PNG data, which a PNG already is. It is `None`, as Pi returns null,
/// when the data does not decode. It may be called before `run`.
pub fn convert_to_png(base64_data: &str, mime_type: &str) -> io::Result<Option<ConvertedImage>> {
    decode_converted(helper_call(CALL_CONVERT_TO_PNG, json!({"data": base64_data, "mimeType": mime_type}))?)
}

/// Pi's `resizeImage` (`utils/image-resize.ts`, exported from `index.ts`): the image fitted within the option limits (2000 pixels on a side, 4.5 MiB encoded unless
/// `options` say otherwise), as the host's image code does it. It is `None`, as Pi returns null, when the image cannot be made to fit. An image already within every limit
/// comes back as it was. It may be called before `run`.
pub fn resize_image(input: &[u8], mime_type: &str, options: Option<&ImageResizeOptions>) -> io::Result<Option<crate::ResizedImage>> {
    decode_resized(helper_call(CALL_RESIZE_IMAGE, resize_args(input, mime_type, options))?)
}

pub(crate) fn generate_diff_string_args(old_content: &str, new_content: &str, context_lines: Option<i64>) -> (&'static str, Value) {
    (CALL_GENERATE_DIFF_STRING, diff_args(old_content, new_content, context_lines))
}

pub(crate) fn generate_unified_patch_args(path: &str, old_content: &str, new_content: &str, context_lines: Option<i64>) -> (&'static str, Value) {
    (CALL_GENERATE_UNIFIED_PATCH, patch_args(path, old_content, new_content, context_lines))
}

pub(crate) fn copy_to_clipboard_args(text: &str) -> (&'static str, Value) {
    (CALL_COPY_TO_CLIPBOARD, json!({"text": text}))
}

pub(crate) fn convert_to_png_args(base64_data: &str, mime_type: &str) -> (&'static str, Value) {
    (CALL_CONVERT_TO_PNG, json!({"data": base64_data, "mimeType": mime_type}))
}

pub(crate) fn resize_image_args(input: &[u8], mime_type: &str, options: Option<&ImageResizeOptions>) -> (&'static str, Value) {
    (CALL_RESIZE_IMAGE, resize_args(input, mime_type, options))
}

/// The result of a host call made outside a handler: over the running extension's connection, or over the one this call dials, which `run` reuses.
pub(crate) fn helper_call(method: &str, args: Value) -> io::Result<Option<Value>> {
    let running = running_for(&locked(&RUNNING), socket_path().as_deref());
    let conn = match running {
        Some(conn) => conn,
        None => dial_loading()?,
    };
    let pending = conn.begin_call_for(None, method, Some(args))?;
    finish(conn.wait_call(pending)?)
}

/// The connection a factory's host calls use before `run`: the one the first of them dials to this extension's socket, which `run` takes. It is never a running
/// extension's: the host attributes what a call carries (a `pi.events` listener) to the extension of the connection it arrives on. No reader thread serves it, so
/// a call on it reads the frames itself ([`Connection::is_loading`]).
pub(crate) fn dial_loading() -> io::Result<Arc<Connection>> {
    let path = match socket_path() {
        Some(path) if !path.is_empty() => path,
        _ => return Err(io::Error::new(io::ErrorKind::NotConnected, "not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig")),
    };
    let mut dialled = locked(&DIALLED);
    if let Some((_, conn)) = dialled.iter().find(|(dialled_path, _)| *dialled_path == path) {
        return Ok(conn.clone());
    }
    let conn = Arc::new(Connection::new(UnixStream::connect(&path)?));
    conn.set_loading(true);
    dialled.push((path, conn.clone()));
    Ok(conn)
}

/// The result of a host call over the first running extension's connection that answers; the last failure when none does. It never dials: it is for a call that
/// must reach the host after the connection it started on has gone.
pub(crate) fn helper_call_each_running(method: &str, args: Value) -> io::Result<Option<Value>> {
    let connections: Vec<Arc<Connection>> = locked(&RUNNING).iter().filter_map(|(_, weak)| weak.upgrade()).collect();
    first_answer(&connections, |conn| conn.begin_call_for(None, method, Some(args.clone())).and_then(|pending| conn.wait_call(pending)).and_then(finish))
}

/// The first answer `call` gets from `connections`, tried in order; the last failure when none answers, `NotConnected` when there are none.
fn first_answer<C>(connections: &[C], mut call: impl FnMut(&C) -> io::Result<Option<Value>>) -> io::Result<Option<Value>> {
    let mut failure = io::Error::new(io::ErrorKind::NotConnected, "no extension is running");
    for conn in connections {
        match call(conn) {
            Ok(result) => return Ok(result),
            Err(error) => failure = error,
        }
    }
    Err(failure)
}

fn finish(result: crate::protocol::CallResultMsg) -> io::Result<Option<Value>> {
    if let Some(err) = result.error {
        return Err(io::Error::other(crate::context::host_error_message(err)));
    }
    Ok(result.result)
}

/// The socket a helper dials: `PIG_EXT_SOCKET`, which a packed cell's runner sets to the member's socket while the member's factory runs.
fn socket_path() -> Option<String> {
    std::env::var("PIG_EXT_SOCKET").or_else(|_| std::env::var("GOPI_EXT_SOCKET")).ok()
}

/// The connection a helper opened to `path`, for `run` to serve on.
pub(crate) fn take_dialled(path: &str) -> Option<Arc<Connection>> {
    let mut dialled = locked(&DIALLED);
    let at = dialled.iter().position(|(dialled_path, _)| dialled_path == path)?;
    Some(dialled.remove(at).1)
}

/// Makes the connection of a running extension the one helpers use outside a factory, so a helper called from code that has no [`crate::Context`] does not
/// dial a socket the host already accepted.
pub(crate) fn adopt(conn: &Arc<Connection>, path: &str) {
    add_running(&mut locked(&RUNNING), conn, path);
}

/// The connection a helper uses among `running`. A runner that names a socket (`PIG_EXT_SOCKET`, set to the member's own while its factory runs) means the
/// extension that socket belongs to, so a helper called by one member's factory never rides another member's connection, which the host serves only after
/// it has accepted this member's: that is the running extension on `path`, and none while this member has not connected (the helper dials it). With no
/// socket named, the first extension that is still running answers.
fn running_for(running: &[(String, Weak<Connection>)], path: Option<&str>) -> Option<Arc<Connection>> {
    match path.filter(|path| !path.is_empty()) {
        Some(path) => running.iter().filter(|(served, _)| served == path).find_map(|(_, weak)| weak.upgrade()),
        None => running.iter().find_map(|(_, weak)| weak.upgrade()),
    }
}

fn add_running(running: &mut Vec<(String, Weak<Connection>)>, conn: &Arc<Connection>, path: &str) {
    running.retain(|(_, weak)| weak.strong_count() > 0);
    running.push((path.to_string(), Arc::downgrade(conn)));
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::os::unix::net::UnixStream;

    // A packed cell runs several extensions in one process. A helper called with no Context and no socket of its own uses a running extension's connection,
    // and must keep working when the first one to start has stopped.
    #[test]
    fn a_helper_keeps_a_connection_when_the_first_running_extension_stops() {
        let (a, _a_peer) = UnixStream::pair().unwrap();
        let (b, _b_peer) = UnixStream::pair().unwrap();
        let first = Arc::new(Connection::new(a));
        let second = Arc::new(Connection::new(b));
        let mut running = Vec::new();
        add_running(&mut running, &first, "/s/e-0.sock");
        add_running(&mut running, &second, "/s/e-1.sock");
        let live = |running: &[(String, Weak<Connection>)]| running_for(running, None);
        assert!(Arc::ptr_eq(&live(&running).unwrap(), &first));
        drop(first);
        assert!(Arc::ptr_eq(&live(&running).unwrap(), &second));
        drop(second);
        assert!(live(&running).is_none());
    }

    // The runner names a member's socket while that member's factory runs. The host accepts a packed cell's members one at a time, in order, and serves a
    // connection only after the members before it: a helper called by the first member's factory that rode the second member's registered connection waited
    // for an answer the host would not send until the first member had connected.
    #[test]
    fn a_helper_named_for_a_socket_does_not_use_another_members_connection() {
        let (a, _a_peer) = UnixStream::pair().unwrap();
        let (b, _b_peer) = UnixStream::pair().unwrap();
        let first = Arc::new(Connection::new(a));
        let second = Arc::new(Connection::new(b));
        let mut running = Vec::new();
        add_running(&mut running, &second, "/s/e-1.sock");
        assert!(running_for(&running, Some("/s/e-0.sock")).is_none(), "the first member has not connected: its helper dials its own socket");
        assert!(Arc::ptr_eq(&running_for(&running, Some("/s/e-1.sock")).unwrap(), &second));
        add_running(&mut running, &first, "/s/e-0.sock");
        assert!(Arc::ptr_eq(&running_for(&running, Some("/s/e-0.sock")).unwrap(), &first));
        assert!(Arc::ptr_eq(&running_for(&running, Some("")).unwrap(), &second), "an empty name is no name");
    }

    // The turn of the file mutation queue belongs to the process: an exit whose connection is stopping goes to the next running extension's.
    #[test]
    fn a_call_that_must_reach_the_host_tries_each_running_connection_until_one_answers() {
        let tried = std::cell::RefCell::new(Vec::new());
        let answer = first_answer(&["stopped", "running", "never reached"], |name| {
            tried.borrow_mut().push(*name);
            if *name == "stopped" { Err(io::Error::other("extension connection is closed")) } else { Ok(Some(json!({"from": name}))) }
        });
        assert_eq!(answer.unwrap(), Some(json!({"from": "running"})));
        assert_eq!(*tried.borrow(), vec!["stopped", "running"]);
        let none: [&str; 0] = [];
        assert_eq!(first_answer(&none, |_| Ok(None)).unwrap_err().kind(), io::ErrorKind::NotConnected);
        let failure = first_answer(&["a", "b"], |name| Err(io::Error::other(format!("{name} failed")))).unwrap_err();
        assert_eq!(failure.to_string(), "b failed");
    }

    // The values are what Node 24.22.1 (V8) answers for Date.parse of each text.
    const CANONICAL: [(&str, f64); 15] = [
        ("1970-01-01T00:00:00.000Z", 0.0),
        ("0000-01-01T00:00:00.000Z", -62167219200000.0),
        ("0000-03-01T00:00:00.000Z", -62162035200000.0),
        ("0001-01-01T00:00:00.000Z", -62135596800000.0),
        ("9999-12-31T23:59:59.999Z", 253402300799999.0),
        ("2000-02-29T12:34:56.789Z", 951827696789.0),
        ("1900-02-28T23:59:59.999Z", -2203891200001.0),
        ("1900-03-01T00:00:00.000Z", -2203891200000.0),
        ("2100-03-01T00:00:00.000Z", 4107542400000.0),
        ("2024-02-29T00:00:00.000Z", 1709164800000.0),
        ("2026-10-24T18:30:15.042Z", 1792866615042.0),
        ("1969-12-31T23:59:59.999Z", -1.0),
        ("1600-02-29T00:00:00.000Z", -11670998400000.0),
        ("2026-12-31T23:59:59.999Z", 1798761599999.0),
        ("2026-01-01T00:00:00.001Z", 1767225600001.0),
    ];

    // Texts V8 reads that the local parser must leave to the host: another form of the same instant, a field the legacy parser accepts although it is out of range
    // (February 30 is March 2), and text that is not a date.
    const LEFT_TO_THE_HOST: [&str; 27] = [
        "2026-01-02T03:04:05.678", "2026-01-02T03:04:05.678+00:00", "+012026-01-02T03:04:05.678Z", "-000001-01-01T00:00:00.000Z", "2026-01-02t03:04:05.678Z",
        "2026-01-02T03:04:05.678z", "2026-02-30T00:00:00.000Z", "2026-13-01T00:00:00.000Z", "2026-01-02T24:00:00.000Z", "2026-01-02T03:04:60.000Z",
        "2026-01-02T03:60:05.000Z", "2026-00-10T00:00:00.000Z", "2026-04-31T00:00:00.000Z", "2026-01-02 03:04:05.678Z", "2026-01-02T03:04:05.678Z\n",
        "２０２６-01-02T03:04:05.678Z", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05.67Z", "2026-01-02T03:04:05.678Zx", "", "2026", "2026-01-02T0x:04:05.678Z",
        "2026-01-02T03:04:05.67:Z", "2026-01-02T03:04:0/.678Z", "202:-01-02T03:04:05.678Z", "2026-01-02T03:04:05.@78Z", "2026-01-00T00:00:00.000Z",
    ];

    #[test]
    fn parse_iso_date_matches_date_parse() {
        for (text, want) in CANONICAL {
            assert_eq!(parse_iso_date(text), Some(want), "{text:?}");
            // With no connection to the host, an answer can only be the local one.
            assert_eq!(parse_date(text).unwrap(), Some(want), "{text:?}");
        }
    }

    #[test]
    fn parse_iso_date_leaves_other_text_to_the_host() {
        for text in LEFT_TO_THE_HOST {
            assert_eq!(parse_iso_date(text), None, "{text:?}");
        }
    }
}
