//! montyrs-worker: a long-lived process that runs untrusted Python in
//! Pydantic's Monty interpreter (the `monty` crate, natively) for the mcpgw
//! gateway, one script at a time.
//!
//! Protocol: frames on stdin/stdout, each a 4-byte big-endian length followed
//! by a JSON object with a "type" field.
//!
//!   gateway -> worker: run, result (reply to a call), abort
//!   worker -> gateway: call (call_tool / call_tool_async), done (exactly one per run)
//!
//! Every run gets a fresh MontyRun whose heap is dropped before "done" is sent,
//! so no state survives between runs. The only capabilities are the two tool
//! functions; every OS call except sleep is answered with PermissionError.

mod confine;
mod convert;
mod memory;
mod protocol;
mod script;

use std::sync::{Arc, Barrier, mpsc};
use std::thread;

use protocol::{read_frame, send, Inbound, Outbound};

/// Counts every byte the process allocates, so Monty's soft memory limit
/// (MemoryError) works, and exits with OOM_EXIT_CODE (65) past the hard one.
#[global_allocator]
static ALLOC: memory::PeakAllocator = memory::PeakAllocator(monty_alloc::LimitedAllocator);

/// The interpreter runs on its own thread with a known stack size.
const ENGINE_THREAD_STACK_BYTES: usize = 64 << 20;

fn main() {
    confine::relaunch_if_unconfined();
    // Confinement waits until both threads have started (glibc registers rseq
    // and robust futexes and sets up the thread's malloc arena there), and the
    // reader waits for confinement, so no frame is read before lock-down.
    let started = Arc::new(Barrier::new(3));
    let confined = Arc::new(Barrier::new(2));
    let inbox = spawn_reader(Arc::clone(&started), Arc::clone(&confined));
    let engine_started = Arc::clone(&started);
    let engine = thread::Builder::new()
        .stack_size(ENGINE_THREAD_STACK_BYTES)
        .spawn(move || {
            engine_started.wait();
            serve(inbox)
        })
        .expect("spawn engine thread");
    started.wait();
    if let Err(e) = confine::apply() {
        eprintln!("montyrs-worker: confinement failed, refusing to run: {e}");
        std::process::exit(3);
    }
    confined.wait();
    let _ = engine.join();
}

fn serve(inbox: mpsc::Receiver<Inbound>) {
    while let Ok(msg) = inbox.recv() {
        // Stale result/abort messages from a finished run are ignored here.
        if let Inbound::Run(req) = msg {
            memory::begin_run();
            let outcome = script::execute(&req, &inbox);
            send(&Outbound::Done { kind: outcome.kind, payload: &outcome.payload, heavy: memory::run_was_heavy() });
        }
    }
}

/// The reader thread owns stdin and forwards every frame to the engine.
fn spawn_reader(started: Arc<Barrier>, confined: Arc<Barrier>) -> mpsc::Receiver<Inbound> {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        started.wait();
        confined.wait();
        let mut stdin = std::io::stdin().lock();
        while let Ok(Some(frame)) = read_frame(&mut stdin) {
            let Ok(msg) = serde_json::from_slice::<Inbound>(&frame) else {
                eprintln!("montyrs-worker: malformed frame from gateway");
                std::process::exit(2);
            };
            if tx.send(msg).is_err() {
                break;
            }
        }
        // EOF on stdin: dropping tx ends the main loop.
    });
    rx
}
