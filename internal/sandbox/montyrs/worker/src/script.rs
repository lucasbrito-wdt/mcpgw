//! Drives one script through Monty's suspend/resume API.
//!
//! The script reaches the gateway through two external functions:
//!   call_tool(name, args=None)        -> blocks the interpreter until the reply
//!   call_tool_async(name, args=None)  -> returns an awaitable at once; replies
//!                                        are fed back through ResolveFutures,
//!                                        so asyncio.gather overlaps the calls.
//! A failed call raises a catchable RuntimeError at the call (or the await).

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc;
use std::time::{Duration, Instant};

use monty::{FunctionCall, MontyRun, OsCall, RunProgress};
use monty_types::{
    memory_limit_with_headroom, CallArgs, CompileOptions, ExcType, ExtFunctionResult, MontyException, MontyObject,
    NameLookupResult, OsFunctionCall, PrintWriter, ResourceLimits, ResourceTracker,
};

use crate::convert::{from_json, to_json, ToJsonError};
use crate::protocol::{send, Inbound, Outbound, RunRequest};

const SYNC_CALL: &str = "call_tool";
const ASYNC_CALL: &str = "call_tool_async";

/// Host round trips (calls, lookups, OS calls, future batches) per run. Monty
/// leaves this budget to the host; it bounds a script that loops on failing
/// calls while its execution clock is paused.
const MAX_SUSPENSIONS: usize = 1000;

/// Caps the JSON arguments of one tool call (the gateway kills a worker that
/// sends more), well below the run's memory.
const MAX_CALL_ARGS_BYTES: usize = 1 << 20;

/// Caps the payload of a failed run; the gateway passes it to the model.
const MAX_ERROR_BYTES: usize = 4 << 10;

/// Call ids are unique for the life of the process, so a late reply can never
/// answer a call of a later run.
static NEXT_CALL_ID: AtomicU64 = AtomicU64::new(1);

pub struct Outcome {
    pub kind: &'static str,
    pub payload: String,
}

impl Outcome {
    fn new(kind: &'static str, payload: impl Into<String>) -> Self {
        let mut payload = payload.into();
        if kind != "ok" {
            payload = clip_middle(payload, MAX_ERROR_BYTES);
        }
        Outcome { kind, payload }
    }
}

/// Why a run ended while the worker was waiting on the gateway.
enum Stop {
    Timeout,
    Aborted,
}

type Step = Result<RunProgress, MontyException>;

/// Runs one script. The interpreter heap is dropped before this returns.
pub fn execute(req: &RunRequest, inbox: &mpsc::Receiver<Inbound>) -> Outcome {
    // Arming here also re-measures the worker baseline the soft limit is relative to.
    if let Err(e) = monty_alloc::set_hard_limit(memory_limit_with_headroom(Some(req.memory_bytes), false)) {
        return Outcome::new("error", format!("arm memory limit: {e}"));
    }
    let timeout = Duration::from_millis(req.timeout_ms);
    let runner = match MontyRun::new(req.code.clone(), "main.py", vec![], CompileOptions::default()) {
        Ok(runner) => runner,
        Err(e) => return error_outcome(&e),
    };
    let limits = ResourceLimits {
        max_feed_duration: Some(timeout),
        max_turn_duration: Some(timeout),
        max_memory: Some(req.memory_bytes),
        max_suspensions: MAX_SUSPENSIONS,
        ..ResourceLimits::default()
    };
    let mut host = Host {
        inbox,
        deadline: Instant::now() + timeout,
        max_args_bytes: req.memory_bytes.min(MAX_CALL_ARGS_BYTES),
        pending: HashMap::new(),
        ready: Vec::new(),
    };
    let first = runner.start(vec![], ResourceTracker::new(limits), PrintWriter::Disabled);
    host.drive(first, req.max_result_bytes)
}

struct Host<'a> {
    inbox: &'a mpsc::Receiver<Inbound>,
    deadline: Instant,
    max_args_bytes: usize,
    /// Wire id -> Monty call id of every call_tool_async still in flight.
    pending: HashMap<u64, u32>,
    /// Async replies received but not yet handed to the interpreter.
    ready: Vec<(u32, ExtFunctionResult)>,
}

impl Host<'_> {
    fn drive(&mut self, mut progress: Step, max_result_bytes: usize) -> Outcome {
        for _ in 0..=MAX_SUSPENSIONS {
            progress = match progress {
                Err(e) => return error_outcome(&e),
                Ok(RunProgress::Complete(value)) => return finish(&value, max_result_bytes),
                Ok(suspended) => match self.step(suspended) {
                    Ok(next) => next,
                    Err(Stop::Timeout) => return Outcome::new("timeout", ""),
                    Err(Stop::Aborted) => return Outcome::new("aborted", ""),
                },
            };
        }
        match progress {
            Ok(RunProgress::Complete(value)) => finish(&value, max_result_bytes),
            Ok(suspended) => match abort(suspended) {
                Err(e) => error_outcome(&e),
                Ok(_) => Outcome::new("error", "RuntimeError: too many host suspensions"),
            },
            Err(e) => error_outcome(&e),
        }
    }

    fn step(&mut self, progress: RunProgress) -> Result<Step, Stop> {
        Ok(match progress {
            RunProgress::NameLookup(lookup) => {
                let answer = match lookup.name.as_str() {
                    SYNC_CALL | ASYNC_CALL => NameLookupResult::Value(MontyObject::function(lookup.name.clone(), None)),
                    _ => NameLookupResult::Undefined,
                };
                lookup.resume(answer, PrintWriter::Disabled)
            }
            RunProgress::FunctionCall(call) => self.call(call)?,
            RunProgress::OsCall(call) => self.os_call(call)?,
            RunProgress::ResolveFutures(waiting) => {
                let results = self.wait_futures(waiting.pending_call_ids())?;
                waiting.resume(results, PrintWriter::Disabled)
            }
            RunProgress::Complete(_) => unreachable!("handled by drive"),
        })
    }

    fn call(&mut self, mut call: FunctionCall) -> Result<Step, Stop> {
        let is_async = match call.function_name.as_str() {
            SYNC_CALL => false,
            ASYNC_CALL => true,
            other => {
                let name = other.to_owned();
                return Ok(call.resume(ExtFunctionResult::NotFound(name), PrintWriter::Disabled));
            }
        };
        let (name, args) = match call_request(&call.args, self.max_args_bytes) {
            Ok(request) => request,
            Err(msg) => return Ok(call.resume(exception(ExcType::TypeError, msg), PrintWriter::Disabled)),
        };
        let id = NEXT_CALL_ID.fetch_add(1, Ordering::Relaxed);
        send(&Outbound::Call { id, name: &name, args: &args });
        if is_async {
            self.pending.insert(id, call.call_id);
            self.refresh_turn_budget(&mut call);
            return Ok(call.resume_pending(PrintWriter::Disabled));
        }
        let result = self.wait_sync(id)?;
        self.refresh_turn_budget(&mut call);
        Ok(call.resume(result, PrintWriter::Disabled))
    }

    /// The feed budget excludes host waits, so a run that waited still gets
    /// its full CPU budget; capping the next turn at the wall-clock time left
    /// keeps a busy loop after a bridge call from outliving the deadline.
    fn refresh_turn_budget(&self, call: &mut FunctionCall) {
        let left = self.deadline.saturating_duration_since(Instant::now());
        call.tracker_mut().set_max_turn_duration(left.max(Duration::from_millis(1)));
    }

    fn os_call(&mut self, call: OsCall) -> Result<Step, Stop> {
        let result: ExtFunctionResult = match &call.function_call {
            OsFunctionCall::Import(module) => ExtFunctionResult::NotFound(module.clone()),
            OsFunctionCall::Sleep(d)
            | OsFunctionCall::SystemSleep(d)
            | OsFunctionCall::AsyncSleep(d)
            | OsFunctionCall::AsyncSystemSleep(d) => {
                self.sleep(*d)?;
                if call.allow_eager_await {
                    return Ok(call.resume_eager(Ok(MontyObject::none()), PrintWriter::Disabled));
                }
                MontyObject::none().into()
            }
            other => exception(
                ExcType::PermissionError,
                format!("os access is not available: {}", other.name()),
            ),
        };
        Ok(call.resume(result, PrintWriter::Disabled))
    }

    fn wait_sync(&mut self, id: u64) -> Result<ExtFunctionResult, Stop> {
        loop {
            if let Some((reply_id, result)) = self.recv(self.deadline)? {
                if reply_id == id {
                    return Ok(result);
                }
                self.stash(reply_id, result);
            }
        }
    }

    /// Returns every available reply for the futures the interpreter is
    /// blocked on, waiting for at least one.
    fn wait_futures(&mut self, pending: &[u32]) -> Result<Vec<(u32, ExtFunctionResult)>, Stop> {
        loop {
            while let Some((reply_id, result)) = self.recv(Instant::now())? {
                self.stash(reply_id, result);
            }
            let (hit, rest) = std::mem::take(&mut self.ready)
                .into_iter()
                .partition(|(call_id, _)| pending.contains(call_id));
            self.ready = rest;
            if !hit.is_empty() {
                return Ok(hit);
            }
            if let Some((reply_id, result)) = self.recv(self.deadline)? {
                self.stash(reply_id, result);
            }
        }
    }

    /// Sleeps without blocking replies or aborts, never past the deadline.
    fn sleep(&mut self, d: Duration) -> Result<(), Stop> {
        let until = (Instant::now() + d).min(self.deadline);
        while let Some((reply_id, result)) = self.recv(until)? {
            self.stash(reply_id, result);
        }
        Ok(())
    }

    fn stash(&mut self, reply_id: u64, result: ExtFunctionResult) {
        // Replies to calls of an earlier run (or ones already answered) are dropped.
        if let Some(call_id) = self.pending.remove(&reply_id) {
            self.ready.push((call_id, result));
        }
    }

    /// Waits until `until` for one reply. Ok(None) means `until` passed
    /// before the run deadline; reaching the deadline is Stop::Timeout.
    fn recv(&self, until: Instant) -> Result<Option<(u64, ExtFunctionResult)>, Stop> {
        let wait = until.saturating_duration_since(Instant::now());
        match self.inbox.recv_timeout(wait) {
            Ok(Inbound::Result { id, ok, error }) => Ok(Some((id, reply(ok, error)))),
            Ok(Inbound::Abort) => Err(Stop::Aborted),
            Ok(Inbound::Run(_)) => {
                eprintln!("montyrs-worker: run received while a run is active");
                std::process::exit(2);
            }
            Err(mpsc::RecvTimeoutError::Timeout) if until >= self.deadline => Err(Stop::Timeout),
            Err(mpsc::RecvTimeoutError::Timeout) => Ok(None),
            Err(mpsc::RecvTimeoutError::Disconnected) => std::process::exit(0),
        }
    }
}

/// Reads call_tool(name, args=None) as (name, JSON args).
fn call_request(args: &CallArgs, max_bytes: usize) -> Result<(String, String), String> {
    if args.args().len() > 2 {
        return Err("call_tool() takes at most 2 positional arguments (name, args)".into());
    }
    let name = args.arg(0).or_else(|| args.kwarg("name"));
    let Some(name) = name.and_then(|n| n.as_str()) else {
        return Err("call_tool() needs a tool name string as its first argument".into());
    };
    let json = match args.arg(1).or_else(|| args.kwarg("args")) {
        None => "null".to_owned(),
        Some(value) => to_json(value, max_bytes).map_err(|e| match e {
            ToJsonError::TooLarge(_) => format!("call_tool() args too large: over {max_bytes} bytes of JSON"),
            ToJsonError::Unserializable(msg) => format!("call_tool() args: {msg}"),
        })?,
    };
    Ok((name.to_owned(), json))
}

fn reply(ok: Option<String>, error: Option<String>) -> ExtFunctionResult {
    if let Some(msg) = error {
        return exception(ExcType::RuntimeError, msg);
    }
    let text = ok.filter(|s| !s.is_empty()).unwrap_or_else(|| "null".to_owned());
    match from_json(&text) {
        Ok(value) => ExtFunctionResult::Return(value),
        Err(e) => exception(ExcType::RuntimeError, format!("bridge returned invalid JSON: {e}")),
    }
}

fn exception(kind: ExcType, msg: impl Into<String>) -> ExtFunctionResult {
    ExtFunctionResult::Error(MontyException::new(kind, Some(msg.into())))
}

/// Ends a run that exhausted MAX_SUSPENSIONS with an uncatchable error.
fn abort(progress: RunProgress) -> Step {
    let exc = MontyException::new(ExcType::RuntimeError, Some("too many host suspensions".into()));
    match progress {
        RunProgress::FunctionCall(c) => c.abort(exc, PrintWriter::Disabled),
        RunProgress::OsCall(c) => c.abort(exc, PrintWriter::Disabled),
        RunProgress::NameLookup(l) => l.abort(exc, PrintWriter::Disabled),
        RunProgress::ResolveFutures(r) => r.abort(exc, PrintWriter::Disabled),
        done @ RunProgress::Complete(_) => Ok(done),
    }
}

fn finish(value: &MontyObject, max_result_bytes: usize) -> Outcome {
    match to_json(value.as_ref(), max_result_bytes) {
        Ok(json) => Outcome::new("ok", json),
        Err(ToJsonError::TooLarge(n)) => Outcome::new("too_large", n.to_string()),
        Err(ToJsonError::Unserializable(msg)) => Outcome::new("unserializable", msg),
    }
}

/// Monty's resource errors are uncatchable exceptions of these types.
fn error_outcome(e: &MontyException) -> Outcome {
    let is_time_limit = e.message().is_some_and(|m| m.contains("time limit exceeded"));
    match e.exc_type() {
        ExcType::MemoryError => Outcome::new("memory", e.summary()),
        ExcType::TimeoutError if is_time_limit => Outcome::new("timeout", e.summary()),
        _ => Outcome::new("error", e.to_string()),
    }
}

/// Keeps the head and tail of text (a traceback's last line names the error)
/// within about max bytes, cutting on char boundaries.
fn clip_middle(text: String, max: usize) -> String {
    if text.len() <= max {
        return text;
    }
    let head = text.floor_char_boundary(max / 2);
    let tail = text.ceil_char_boundary(text.len() - max / 2);
    format!("{}\n... [{} bytes truncated] ...\n{}", &text[..head], tail - head, &text[tail..])
}
