use std::io::{self, Read, Write};

use serde::{Deserialize, Serialize};

#[derive(Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Inbound {
    Run(RunRequest),
    Result {
        id: u64,
        ok: Option<String>,
        error: Option<String>,
    },
    Abort,
}

#[derive(Deserialize)]
pub struct RunRequest {
    pub code: String,
    pub memory_bytes: usize,
    pub timeout_ms: u64,
    pub max_result_bytes: usize,
}

#[derive(Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Outbound<'a> {
    Call { id: u64, name: &'a str, args: &'a str },
    Done { kind: &'a str, payload: &'a str, heavy: bool },
}

/// Reads one length-prefixed frame. Ok(None) means a clean EOF.
pub fn read_frame(r: &mut impl Read) -> io::Result<Option<Vec<u8>>> {
    let mut len = [0u8; 4];
    match r.read_exact(&mut len) {
        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => return Ok(None),
        Err(e) => return Err(e),
        Ok(()) => {}
    }
    let mut buf = vec![0u8; u32::from_be_bytes(len) as usize];
    r.read_exact(&mut buf)?;
    Ok(Some(buf))
}

/// Writes one frame to stdout. Only the engine thread writes, and the
/// interpreter never does: print() is disabled. A broken pipe means the
/// gateway is gone, so the worker exits.
pub fn send(msg: &Outbound) {
    let body = serde_json::to_vec(msg).expect("outbound messages always serialize");
    let mut out = io::stdout().lock();
    let written = out
        .write_all(&(body.len() as u32).to_be_bytes())
        .and_then(|_| out.write_all(&body))
        .and_then(|_| out.flush());
    if written.is_err() {
        std::process::exit(0);
    }
}
