//! OS-level confinement, defense in depth behind Monty's own PermissionError:
//! if the interpreter is ever escaped, native code still cannot open files,
//! reach the network, or start processes.
//!
//! `apply` must run once every thread exists and before the first frame is
//! read. After it, the process can only use the fds it already holds (stdin,
//! stdout, stderr), allocate memory, wait, read the clock and exit.

/// stdin, stdout and stderr are the only fds the worker ever holds.
#[cfg(unix)]
const MAX_OPEN_FILES: libc::rlim_t = 3;

/// Windows can only confine a process at creation (see confine_windows.rs):
/// there, an unconfined process re-launches itself confined and exits with the
/// child's exit code. Call it first thing in main. Elsewhere it does nothing.
pub fn relaunch_if_unconfined() {
    #[cfg(windows)]
    os::relaunch_if_unconfined();
}

/// Confines the whole process, all threads included. An Err means the
/// process is not (fully) confined and must not run scripts.
pub fn apply() -> Result<(), String> {
    #[cfg(unix)]
    {
        close_inherited_fds()?;
        limit_resources()?;
    }
    // Last: the seccomp filter does not allow setrlimit/prlimit64.
    sandbox()
}

/// Closes every fd above stderr, so nothing the parent leaked stays usable.
#[cfg(unix)]
fn close_inherited_fds() -> Result<(), String> {
    let fds: Vec<libc::c_int> = std::fs::read_dir("/dev/fd")
        .map_err(|e| format!("list /dev/fd: {e}"))?
        .filter_map(|entry| entry.ok()?.file_name().to_str()?.parse().ok())
        .filter(|&fd| fd > 2)
        .collect();
    // The listing's own fd is among them and already closed: EBADF is fine.
    for fd in fds {
        unsafe { libc::close(fd) };
    }
    Ok(())
}

#[cfg(unix)]
fn limit_resources() -> Result<(), String> {
    let limits = [
        (libc::RLIMIT_CORE, 0, "RLIMIT_CORE"),
        (libc::RLIMIT_FSIZE, 0, "RLIMIT_FSIZE"),
        (libc::RLIMIT_NOFILE, MAX_OPEN_FILES, "RLIMIT_NOFILE"),
        (libc::RLIMIT_NPROC, 0, "RLIMIT_NPROC"),
    ];
    for (resource, value, name) in limits {
        let lim = libc::rlimit { rlim_cur: value, rlim_max: value };
        if unsafe { libc::setrlimit(resource, &lim) } != 0 {
            return Err(format!("setrlimit({name}): {}", std::io::Error::last_os_error()));
        }
    }
    Ok(())
}

/// seccomp-bpf on Linux, Seatbelt on macOS; on Windows, a check that the
/// confinement the process was created with is in place.
pub fn sandbox() -> Result<(), String> {
    os::apply()
}

#[cfg(target_os = "linux")]
mod os {
    use std::collections::BTreeMap;

    use seccompiler::{
        BpfProgram, SeccompAction, SeccompCmpArgLen, SeccompCmpOp, SeccompCondition, SeccompFilter, SeccompRule,
    };

    // seccompiler (rust-vmm, Firecracker's jailer) over a hand-built BPF: it
    // emits the arch check, argument comparisons and TSYNC install that a
    // hand-written program would have to get right by itself.
    pub fn apply() -> Result<(), String> {
        if unsafe { libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) } != 0 {
            return Err(format!("prctl(PR_SET_NO_NEW_PRIVS): {}", std::io::Error::last_os_error()));
        }
        // The allowlist goes last: seccompiler calls prctl before each install.
        let filters = [open_denial(), allowlist()];
        for filter in filters {
            let program = filter.map_err(|e| format!("build seccomp filter: {e}"))?;
            // TSYNC: the reader and engine threads already exist and must be covered too.
            seccompiler::apply_filter_all_threads(&program).map_err(|e| format!("install seccomp filter: {e}"))?;
        }
        Ok(())
    }

    /// Anything not listed kills the whole process.
    fn allowlist() -> Result<BpfProgram, seccompiler::BackendError> {
        let unconditional = [
            libc::SYS_munmap,
            libc::SYS_mremap,
            libc::SYS_brk,
            libc::SYS_madvise,
            libc::SYS_futex,
            libc::SYS_sched_yield,
            libc::SYS_clock_gettime,
            libc::SYS_clock_nanosleep,
            libc::SYS_nanosleep,
            libc::SYS_getrandom,
            // glibc sizes malloc arenas with it; its fallback would open /sys.
            libc::SYS_sched_getaffinity,
            libc::SYS_rt_sigreturn,
            libc::SYS_rt_sigprocmask,
            libc::SYS_sigaltstack,
            libc::SYS_getpid,
            libc::SYS_gettid,
            libc::SYS_exit,
            libc::SYS_exit_group,
            // Passed here only to reach open_denial, which answers EPERM.
            libc::SYS_openat,
            #[cfg(target_arch = "x86_64")]
            libc::SYS_open,
        ];
        let mut rules: BTreeMap<i64, Vec<SeccompRule>> = unconditional.into_iter().map(|nr| (nr, vec![])).collect();
        rules.insert(libc::SYS_read, vec![arg_eq(0, 0)?]);
        rules.insert(libc::SYS_write, vec![arg_eq(0, 1)?, arg_eq(0, 2)?]);
        // No new executable memory: an escape cannot map shellcode.
        rules.insert(libc::SYS_mmap, vec![no_exec_prot()?]);
        rules.insert(libc::SYS_mprotect, vec![no_exec_prot()?]);
        // abort() (panic = "abort") raises SIGABRT on itself; any other target is denied.
        rules.insert(libc::SYS_tgkill, vec![arg_eq(0, std::process::id() as u64)?]);
        SeccompFilter::new(
            rules,
            SeccompAction::KillProcess,
            SeccompAction::Allow,
            std::env::consts::ARCH.try_into()?,
        )?
        .try_into()
    }

    /// open/openat fail with EPERM instead of killing: std and glibc probe
    /// files on lazy paths (available_parallelism reads cgroup limits on a
    /// script's first regex) and degrade gracefully when that fails. The
    /// kernel applies the strictest verdict of all filters, so EPERM wins.
    fn open_denial() -> Result<BpfProgram, seccompiler::BackendError> {
        let open_calls = [
            libc::SYS_openat,
            #[cfg(target_arch = "x86_64")]
            libc::SYS_open,
        ];
        SeccompFilter::new(
            open_calls.into_iter().map(|nr| (nr, vec![])).collect(),
            SeccompAction::Allow,
            SeccompAction::Errno(libc::EPERM as u32),
            std::env::consts::ARCH.try_into()?,
        )?
        .try_into()
    }

    fn arg_eq(index: u8, value: u64) -> Result<SeccompRule, seccompiler::BackendError> {
        SeccompRule::new(vec![SeccompCondition::new(index, SeccompCmpArgLen::Dword, SeccompCmpOp::Eq, value)?])
    }

    fn no_exec_prot() -> Result<SeccompRule, seccompiler::BackendError> {
        let prot_exec = libc::PROT_EXEC as u64;
        SeccompRule::new(vec![SeccompCondition::new(2, SeccompCmpArgLen::Dword, SeccompCmpOp::MaskedEq(prot_exec), 0)?])
    }
}

#[cfg(target_os = "macos")]
mod os {
    use std::ffi::{CStr, c_char, c_int};

    /// Seatbelt (SBPL): deny everything. Already-open fds keep working, and
    /// malloc, threads, clocks and getentropy do not go through Seatbelt.
    const PROFILE: &CStr = c"(version 1) (deny default)";

    unsafe extern "C" {
        fn sandbox_init(profile: *const c_char, flags: u64, errorbuf: *mut *mut c_char) -> c_int;
        fn sandbox_free_error(errorbuf: *mut c_char);
    }

    pub fn apply() -> Result<(), String> {
        let mut err: *mut c_char = std::ptr::null_mut();
        // flags = 0: PROFILE is the SBPL source itself, not a named profile.
        if unsafe { sandbox_init(PROFILE.as_ptr(), 0, &mut err) } == 0 {
            return Ok(());
        }
        let msg = if err.is_null() {
            "unknown error".to_string()
        } else {
            let msg = unsafe { CStr::from_ptr(err) }.to_string_lossy().into_owned();
            unsafe { sandbox_free_error(err) };
            msg
        };
        Err(format!("sandbox_init: {msg}"))
    }
}

#[cfg(windows)]
#[path = "confine_windows.rs"]
mod os;

#[cfg(not(any(target_os = "linux", target_os = "macos", windows)))]
mod os {
    pub fn apply() -> Result<(), String> {
        Err("no OS confinement for this platform".into())
    }
}
