use crate::context::SessionMirror;
use serde_json::{Value, json};
use std::alloc::{GlobalAlloc, Layout, System};
use std::cell::Cell;
use std::sync::Arc;
use std::sync::atomic::Ordering;

// Counts the allocations of the calling thread, so a test reads its own cost whatever the other tests are doing.
struct CountingAlloc;

thread_local! {
    static ALLOCATIONS: Cell<u64> = const { Cell::new(0) };
}

unsafe impl GlobalAlloc for CountingAlloc {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        let _ = ALLOCATIONS.try_with(|count| count.set(count.get() + 1));
        unsafe { System.alloc(layout) }
    }
    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        unsafe { System.dealloc(ptr, layout) }
    }
    unsafe fn realloc(&self, ptr: *mut u8, layout: Layout, new_size: usize) -> *mut u8 {
        let _ = ALLOCATIONS.try_with(|count| count.set(count.get() + 1));
        unsafe { System.realloc(ptr, layout, new_size) }
    }
}

#[global_allocator]
static ALLOCATOR: CountingAlloc = CountingAlloc;

fn allocations() -> u64 {
    ALLOCATIONS.with(Cell::get)
}

fn entry(id: &str, parent: Option<&str>) -> Value {
    json!({"type":"message","id":id,"parentId":parent,"timestamp":"2026-10-11T00:00:00.000Z","message":{"role":"user","content":format!("text of {id}")}})
}

fn mirror() -> SessionMirror {
    let mirror = SessionMirror::default();
    mirror.subscribed.store(true, Ordering::Release);
    mirror
}

fn push(mirror: &mut SessionMirror, session: &str, entries: Vec<Value>, total: usize, leaf: &str) {
    mirror.apply_update(&json!({"sessionId": session, "leafId": leaf, "entriesAppended": entries, "entryCount": total}));
}

fn ids(branch: &[Arc<Value>]) -> Vec<String> {
    branch.iter().map(|entry| entry["id"].as_str().unwrap_or("").to_string()).collect()
}

// The root→leaf walk over the log, written without any cache.
fn reference(mirror: &SessionMirror, log: &[(String, Option<String>)], leaf: &str) -> Vec<String> {
    let _ = mirror;
    let mut path = Vec::new();
    let mut current = leaf.to_string();
    while let Some((id, parent)) = log.iter().rev().find(|(id, _)| *id == current) {
        path.push(id.clone());
        match parent {
            Some(parent) => current = parent.clone(),
            None => break,
        }
    }
    path.reverse();
    path
}

// Appends below the leaf, forks from an older entry, leaf moves without appends and session switches read the same branch as an uncached walk, through both readers.
#[test]
fn incremental_branch_matches_an_uncached_walk_across_leaf_moves() {
    let mut mirror = mirror();
    let mut seed: u64 = 7;
    let mut rand = |n: usize| {
        seed = seed.wrapping_mul(6364136223846793005).wrapping_add(1442695040888963407);
        ((seed >> 33) as usize) % n
    };
    let mut session = "s1".to_string();
    let mut log: Vec<(String, Option<String>)> = Vec::new();
    let mut leaf = String::new();
    for step in 0..400 {
        let op = rand(10);
        if op < 5 || log.is_empty() {
            let id = format!("{session}-e{}", log.len());
            let parent = (!leaf.is_empty()).then(|| leaf.clone());
            push(&mut mirror, &session, vec![entry(&id, parent.as_deref())], log.len() + 1, &id);
            log.push((id.clone(), parent));
            leaf = id;
        } else if op == 5 {
            let mut batch = Vec::new();
            let mut parent = leaf.clone();
            for _ in 0..1 + rand(4) {
                let id = format!("{session}-e{}", log.len());
                batch.push(entry(&id, Some(&parent)));
                log.push((id.clone(), Some(parent.clone())));
                parent = id;
            }
            push(&mut mirror, &session, batch, log.len(), &parent);
            leaf = parent;
        } else if op < 8 {
            let parent = log[rand(log.len())].0.clone();
            let id = format!("{session}-e{}", log.len());
            push(&mut mirror, &session, vec![entry(&id, Some(&parent))], log.len() + 1, &id);
            log.push((id.clone(), Some(parent)));
            leaf = id;
        } else if op < 9 {
            leaf = log[rand(log.len())].0.clone();
            push(&mut mirror, &session, vec![], log.len(), &leaf);
        } else {
            session = format!("s{step}");
            let id = format!("{session}-e0");
            push(&mut mirror, &session, vec![entry(&id, None)], 1, &id);
            log = vec![(id.clone(), None)];
            leaf = id;
        }
        if step % 3 == 0 {
            mirror.get_branch_entries();
        }
        let want = reference(&mirror, &log, &leaf);
        assert_eq!(ids(&mirror.branch().to_vec()), want, "step {step}");
        let decoded: Vec<String> = mirror.get_branch_entries().iter().map(|entry| entry.id.clone()).collect();
        assert_eq!(decoded, want, "step {step}");
    }
}

// An entry appended under an unchanged leaf leaves the branch as it was, sharing the entries already decoded; a repeated id is read as the later entry.
#[test]
fn incremental_branch_side_entry_and_repeated_id() {
    let mut mirror = mirror();
    push(&mut mirror, "s", vec![entry("a", None), entry("b", Some("a"))], 2, "b");
    let first = mirror.get_branch_entries();
    push(&mut mirror, "s", vec![entry("side", Some("a"))], 3, "b");
    let second = mirror.get_branch_entries();
    assert_eq!(second.len(), 2);
    assert!(Arc::ptr_eq(&first[0], &second[0]) && Arc::ptr_eq(&first[1], &second[1]));
    push(&mut mirror, "s", vec![entry("b", Some("side"))], 4, "b");
    assert_eq!(ids(&mirror.branch().to_vec()), ["a", "side", "b"]);
    let decoded: Vec<String> = mirror.get_branch_entries().iter().map(|entry| entry.id.clone()).collect();
    assert_eq!(decoded, ["a", "side", "b"]);
}

fn append_and_read_allocations(n: usize) -> u64 {
    let mut mirror = mirror();
    let log: Vec<Value> = (0..n).map(|i| entry(&format!("e{i}"), (i > 0).then(|| format!("e{}", i - 1)).as_deref())).collect();
    push(&mut mirror, "s", log, n, &format!("e{}", n - 1));
    mirror.get_branch_entries();
    let mut best = u64::MAX;
    for round in 0..5 {
        let at = n + round;
        let id = format!("e{at}");
        let appended = vec![entry(&id, Some(&format!("e{}", at - 1)))];
        let update = json!({"sessionId": "s", "leafId": id, "entriesAppended": appended, "entryCount": at + 1});
        let before = allocations();
        mirror.apply_update(&update);
        let branch = mirror.get_branch_entries();
        let after = allocations();
        assert_eq!(branch.len(), at + 1);
        best = best.min(after - before);
    }
    best
}

// Reading the branch after one append costs the new entry and not the branch.
#[test]
fn appending_then_reading_the_branch_allocates_the_same_at_any_length() {
    let (small, large) = (append_and_read_allocations(2000), append_and_read_allocations(16000));
    assert!(large <= small * 3 / 2 + 8, "allocations grew with the branch: {small} at n=2000, {large} at n=16000");
}
