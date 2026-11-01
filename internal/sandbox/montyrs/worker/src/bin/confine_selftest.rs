//! Test-only probe (feature `confine-selftest`): confines itself exactly like
//! montyrs-worker, then makes one escape attempt named by argv[1] and prints
//! "blocked <errno>" or "allowed". Under seccomp a denied syscall kills the
//! process instead, so each attempt runs in its own process.
//!
//! With "sandbox-only" as argv[2] the rlimits are skipped, so the attempt
//! reaches (and proves) the seccomp/Seatbelt layer on its own.
//!
//! "connect" dials the IPv4 "host:port" in argv[3] (default 127.0.0.1:1). The
//! Go test passes a live listener, so a refused connection is a denial too,
//! and it checks the listener never accepted anything.
//!
//! The attempt runs on a thread spawned before confinement, as the worker's
//! reader and engine threads are, so it also proves the filter reached them.
//!
//! On Windows the probe is launched confined (AppContainer, job, mitigations)
//! like the worker, both modes are the same, and the attempts are the Windows
//! equivalents: the user-owned directory of the binary stands in for
//! /etc/passwd and /tmp, "fork" starts this binary again, "exec" starts
//! cmd.exe. "socket" is not an attempt there: AppContainers mediate connect,
//! not socket creation.

#[path = "../confine.rs"]
mod confine;

use std::io::Error;
use std::net::SocketAddrV4;
use std::sync::{Arc, Barrier};
use std::thread;

fn main() {
    confine::relaunch_if_unconfined();
    let attempt = std::env::args().nth(1).unwrap_or_default();
    let sandbox_only = std::env::args().nth(2).as_deref() == Some("sandbox-only");
    let target: SocketAddrV4 = match std::env::args().nth(3).unwrap_or_else(|| "127.0.0.1:1".into()).parse() {
        Ok(target) => target,
        Err(e) => {
            eprintln!("confine-selftest: bad connect target: {e}");
            std::process::exit(2);
        }
    };
    let started = Arc::new(Barrier::new(2));
    let confined = Arc::new(Barrier::new(2));
    let (probe_started, probe_confined) = (Arc::clone(&started), Arc::clone(&confined));
    let probe = thread::spawn(move || {
        probe_started.wait();
        probe_confined.wait();
        println!("{}", run(&attempt, target));
    });
    started.wait();
    let confined_ok = if sandbox_only { confine::sandbox() } else { confine::apply() };
    if let Err(e) = confined_ok {
        eprintln!("confine-selftest: confinement failed: {e}");
        std::process::exit(3);
    }
    confined.wait();
    let _ = probe.join();
}

#[cfg(unix)]
fn run(attempt: &str, target: SocketAddrV4) -> String {
    let rc: i64 = unsafe {
        match attempt {
            "none" => return "ok".into(),
            "open" => libc::open(c"/etc/passwd".as_ptr(), libc::O_RDONLY).into(),
            "create" => libc::open(c"/tmp/confine-selftest".as_ptr(), libc::O_WRONLY | libc::O_CREAT, 0o600).into(),
            "socket" => libc::socket(libc::AF_INET, libc::SOCK_STREAM, 0).into(),
            "connect" => connect_to(target),
            "fork" => fork(),
            "exec" => {
                let argv = [c"echo".as_ptr(), c"allowed".as_ptr(), std::ptr::null()];
                let envp = [std::ptr::null()];
                libc::execve(c"/bin/echo".as_ptr(), argv.as_ptr(), envp.as_ptr()).into()
            }
            "exec-memory" => exec_memory(),
            _ => return format!("unknown attempt {attempt:?}"),
        }
    };
    if rc < 0 { format!("blocked {}", Error::last_os_error()) } else { "allowed".into() }
}

#[cfg(unix)]
unsafe fn connect_to(target: SocketAddrV4) -> i64 {
    let fd = unsafe { libc::socket(libc::AF_INET, libc::SOCK_STREAM, 0) };
    if fd < 0 {
        return -1;
    }
    let mut addr: libc::sockaddr_in = unsafe { std::mem::zeroed() };
    addr.sin_family = libc::AF_INET as libc::sa_family_t;
    addr.sin_port = target.port().to_be();
    addr.sin_addr.s_addr = u32::from(*target.ip()).to_be();
    let len = size_of::<libc::sockaddr_in>() as libc::socklen_t;
    unsafe { libc::connect(fd, (&raw const addr).cast(), len) }.into()
}

#[cfg(unix)]
unsafe fn fork() -> i64 {
    let pid = unsafe { libc::fork() };
    if pid == 0 {
        unsafe { libc::_exit(0) };
    }
    pid.into()
}

#[cfg(unix)]
unsafe fn exec_memory() -> i64 {
    let prot = libc::PROT_READ | libc::PROT_WRITE | libc::PROT_EXEC;
    let flags = libc::MAP_PRIVATE | libc::MAP_ANON;
    let addr = unsafe { libc::mmap(std::ptr::null_mut(), 4096, prot, flags, -1, 0) };
    if addr == libc::MAP_FAILED { -1 } else { 0 }
}

#[cfg(windows)]
fn run(attempt: &str, target: SocketAddrV4) -> String {
    use std::process::Command;

    let exe = std::env::current_exe().expect("current_exe");
    let exe_dir = exe.parent().expect("binary directory");
    let result = match attempt {
        "none" => return "ok".into(),
        "open" => std::fs::read_dir(exe_dir).map(drop),
        "create" => std::fs::File::create(exe_dir.join("confine-selftest.tmp")).map(drop),
        // The timeout bounds a filter that drops packets instead of failing.
        "connect" => std::net::TcpStream::connect_timeout(&target.into(), CONNECT_TIMEOUT).map(drop),
        "fork" => Command::new(&exe).arg("none").status().map(drop),
        // std finds cmd.exe in the system directory even with an empty PATH.
        "exec" => Command::new("cmd.exe").args(["/c", "exit 0"]).status().map(drop),
        "exec-memory" => exec_memory(),
        _ => return format!("unknown attempt {attempt:?}"),
    };
    match result {
        Ok(()) => "allowed".into(),
        Err(e) => format!("blocked {e}"),
    }
}

#[cfg(windows)]
const CONNECT_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(5);

/// Arbitrary Code Guard refuses executable private memory.
#[cfg(windows)]
fn exec_memory() -> std::io::Result<()> {
    use windows_sys::Win32::System::Memory::{MEM_COMMIT, MEM_RESERVE, PAGE_EXECUTE_READWRITE, VirtualAlloc};

    let addr = unsafe { VirtualAlloc(std::ptr::null(), 4096, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE) };
    if addr.is_null() { Err(Error::last_os_error()) } else { Ok(()) }
}
