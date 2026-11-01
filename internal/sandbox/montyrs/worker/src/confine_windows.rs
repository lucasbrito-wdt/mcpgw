//! Windows confinement. seccomp and Seatbelt are applied by a process to
//! itself, but an AppContainer can only be entered at process creation, which
//! Go's os/exec cannot request. So the binary runs twice: started by the
//! gateway it is the launcher, which spawns itself confined and relays the
//! exit code; started by the launcher (its token is an AppContainer token) it
//! runs normally, and `apply` only checks that the confinement is in place.
//! Keeping the launcher here, not in Go, leaves os/exec untouched and keeps
//! every Windows API call next to the code it protects.
//!
//! The confined child gets:
//! - an AppContainer token without capabilities: no network (loopback too),
//!   no user files or registry, only what ALL APPLICATION PACKAGES may read;
//! - a job that dies with the launcher (KILL_ON_JOB_CLOSE), holds one process,
//!   caps committed memory and suppresses crash dialogs. It sets no UI limits:
//!   win32k lockdown already denies every API they cover, and a job with UI
//!   limits cannot be nested, which the gateway's job (and CI runners') needs;
//! - mitigations: no child processes, no dynamic code (ACG), no win32k
//!   syscalls, only Microsoft-signed, local, non-low-label DLLs;
//! - the launcher's stdin, stdout and stderr, and no other handle.

use std::ffi::{CStr, OsStr, OsString, c_void};
use std::marker::PhantomData;
use std::os::windows::ffi::{OsStrExt, OsStringExt};
use std::path::PathBuf;
use std::ptr::{null, null_mut};

use windows_sys::Win32::Foundation::{
    CloseHandle, ERROR_ACCESS_DENIED, GetLastError, HANDLE, HANDLE_FLAG_INHERIT, HMODULE, INVALID_HANDLE_VALUE,
    SetHandleInformation,
};
use windows_sys::Win32::Security::{
    GetTokenInformation, PSID, SECURITY_CAPABILITIES, TOKEN_DUPLICATE, TOKEN_IMPERSONATE, TOKEN_QUERY, TokenIsAppContainer,
};
use windows_sys::Win32::System::Console::{GetStdHandle, STD_ERROR_HANDLE, STD_INPUT_HANDLE, STD_OUTPUT_HANDLE};
use windows_sys::Win32::System::Diagnostics::Debug::{
    SEM_FAILCRITICALERRORS, SEM_NOGPFAULTERRORBOX, SEM_NOOPENFILEERRORBOX, SetErrorMode,
};
use windows_sys::Win32::System::Environment::GetCommandLineW;
use windows_sys::Win32::System::IO::{CreateIoCompletionPort, GetQueuedCompletionStatus, OVERLAPPED};
use windows_sys::Win32::System::JobObjects::{
    AssignProcessToJobObject, CreateJobObjectW, IsProcessInJob, JOB_OBJECT_LIMIT_ACTIVE_PROCESS,
    JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, JOB_OBJECT_LIMIT_PROCESS_MEMORY,
    JOBOBJECT_ASSOCIATE_COMPLETION_PORT, JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JOBOBJECTINFOCLASS,
    JobObjectAssociateCompletionPortInformation, JobObjectExtendedLimitInformation, SetInformationJobObject,
};
use windows_sys::Win32::System::LibraryLoader::{GetProcAddress, LOAD_LIBRARY_SEARCH_SYSTEM32, LoadLibraryExW};
use windows_sys::Win32::System::SystemInformation::GetSystemDirectoryW;
use windows_sys::Win32::System::SystemServices::{JOB_OBJECT_MSG_ACTIVE_PROCESS_ZERO, JOB_OBJECT_MSG_PROCESS_MEMORY_LIMIT};
use windows_sys::Win32::System::Threading::{
    CREATE_SUSPENDED, CreateMutexW, CreateProcessW, DETACHED_PROCESS, DeleteProcThreadAttributeList, EXTENDED_STARTUPINFO_PRESENT,
    GetCurrentProcess, GetExitCodeProcess, GetProcessMitigationPolicy, INFINITE, InitializeProcThreadAttributeList,
    LPPROC_THREAD_ATTRIBUTE_LIST, OpenProcessToken, PROC_THREAD_ATTRIBUTE_CHILD_PROCESS_POLICY,
    PROC_THREAD_ATTRIBUTE_HANDLE_LIST, PROC_THREAD_ATTRIBUTE_MITIGATION_POLICY,
    PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES, PROCESS_INFORMATION, PROCESS_MITIGATION_POLICY,
    ProcessChildProcessPolicy, ProcessDynamicCodePolicy, ProcessImageLoadPolicy, ProcessSignaturePolicy,
    ProcessSystemCallDisablePolicy, ReleaseMutex, ResumeThread, STARTF_USESTDHANDLES, STARTUPINFOEXW, TerminateProcess,
    UpdateProcThreadAttribute, WaitForSingleObject,
};
use windows_sys::Win32::System::WindowsProgramming::PROCESS_CREATION_CHILD_PROCESS_RESTRICTED;
use windows_sys::core::{BOOL, HRESULT, PCWSTR};

/// Deterministic, so every launcher uses the same AppContainer SID.
const APP_CONTAINER_NAME: &str = "mcpgw.montyrs";

/// The gateway passes its per-run memory cap with this flag; the confined
/// child ignores it.
const MEMORY_CAP_FLAG: &str = "--max-memory-bytes=";
/// Used when the flag is absent (confine-selftest): the gateway's default cap.
const DEFAULT_MEMORY_CAP_BYTES: usize = 64 << 20;
/// monty-alloc ends the worker 4 MiB past the cap (above its baseline); the
/// job limit is the backstop for memory monty-alloc does not see.
const JOB_MEMORY_HEADROOM_BYTES: usize = 64 << 20;
/// How long to wait for each job message once the child has exited.
const JOB_MESSAGE_WAIT_MS: u32 = 500;

// winnt.h PROCESS_CREATION_MITIGATION_POLICY_*_ALWAYS_ON (not in windows-sys).
const WIN32K_SYSTEM_CALL_DISABLE: u64 = 1 << 28;
const PROHIBIT_DYNAMIC_CODE: u64 = 1 << 36;
const BLOCK_NON_MICROSOFT_BINARIES: u64 = 1 << 44;
const IMAGE_LOAD_NO_REMOTE: u64 = 1 << 52;
const IMAGE_LOAD_NO_LOW_LABEL: u64 = 1 << 56;
const MITIGATIONS: u64 = WIN32K_SYSTEM_CALL_DISABLE
    | PROHIBIT_DYNAMIC_CODE
    | BLOCK_NON_MICROSOFT_BINARIES
    | IMAGE_LOAD_NO_REMOTE
    | IMAGE_LOAD_NO_LOW_LABEL;

/// The same mitigations as GetProcessMitigationPolicy reports them: each
/// policy is one DWORD of flags.
const REQUIRED_POLICIES: [(PROCESS_MITIGATION_POLICY, u32, &str); 5] = [
    (ProcessDynamicCodePolicy, 0b1, "ProhibitDynamicCode"),
    (ProcessSystemCallDisablePolicy, 0b1, "DisallowWin32kSystemCalls"),
    (ProcessSignaturePolicy, 0b1, "MicrosoftSignedOnly"),
    (ProcessImageLoadPolicy, 0b11, "NoRemoteImages and NoLowMandatoryLabelImages"),
    (ProcessChildProcessPolicy, 0b1, "NoChildProcessCreation"),
];

/// Serializes profile creation across launchers: a profile half created by
/// another launcher has no folder yet, and CreateProcessW fails on it.
const PROFILE_MUTEX_NAME: &str = "Local\\mcpgw.montyrs.profile";

/// The user's standard variables the launcher takes from its default
/// environment block. CreateProcessW resolves an AppContainer's folders from
/// them (without them it fails with ERROR_ENVVAR_NOT_FOUND), and Windows
/// processes expect SystemRoot. Anything user-defined stays out.
const PROFILE_ENV_VARS: [&str; 16] = [
    "ALLUSERSPROFILE",
    "APPDATA",
    "ComSpec",
    "HOMEDRIVE",
    "HOMEPATH",
    "LOCALAPPDATA",
    "ProgramData",
    "ProgramFiles",
    "PUBLIC",
    "SystemDrive",
    "SystemRoot",
    "TEMP",
    "TMP",
    "USERNAME",
    "USERPROFILE",
    "windir",
];

/// ALL APPLICATION PACKAGES, the group every AppContainer token belongs to.
const ALL_APPLICATION_PACKAGES_SID: &str = "S-1-15-2-1";

type CreateProfileFn =
    unsafe extern "system" fn(PCWSTR, PCWSTR, PCWSTR, *const c_void, u32, *mut PSID) -> HRESULT;
type DeriveSidFn = unsafe extern "system" fn(PCWSTR, *mut PSID) -> HRESULT;
type CreateEnvironmentBlockFn = unsafe extern "system" fn(*mut *mut c_void, HANDLE, BOOL) -> BOOL;
type RawProc = unsafe extern "system" fn() -> isize;

/// Returns in the confined child; in the launcher it never returns.
pub fn relaunch_if_unconfined() {
    let result = match is_app_container() {
        Ok(true) => return,
        Ok(false) => launch(),
        Err(e) => Err(e),
    };
    match result {
        Ok(code) => std::process::exit(code as i32),
        Err(e) => {
            eprintln!("confine: refusing to run: {e}");
            std::process::exit(3);
        }
    }
}

/// Checks, from inside the child, every guarantee the launcher set up.
pub fn apply() -> Result<(), String> {
    if !is_app_container()? {
        return Err("not running in an AppContainer".into());
    }
    let mut in_job = 0;
    if unsafe { IsProcessInJob(GetCurrentProcess(), null_mut(), &mut in_job) } == 0 {
        return Err(last_error("IsProcessInJob"));
    }
    if in_job == 0 {
        return Err("not running in a job object".into());
    }
    for (policy, required, name) in REQUIRED_POLICIES {
        if policy_flags(policy)? & required != required {
            return Err(format!("mitigation {name} is off"));
        }
    }
    Ok(())
}

fn launch() -> Result<u32, String> {
    // Inherited by the child; the job also forces SEM_NOGPFAULTERRORBOX.
    unsafe { SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX) };
    let memory_limit = memory_cap()?.saturating_add(JOB_MEMORY_HEADROOM_BYTES);
    let userenv = load_userenv()?;
    set_profile_environment(userenv)?;
    let sid = app_container_sid(userenv)?;
    let (job, port) = create_job(memory_limit)?;
    let child = spawn_suspended(sid)?;
    // Assigned while suspended: the child never runs a single instruction outside the job.
    if unsafe { AssignProcessToJobObject(job, child.hProcess) } == 0 || unsafe { ResumeThread(child.hThread) } == u32::MAX {
        let err = last_error("start confined child");
        unsafe { TerminateProcess(child.hProcess, 3) };
        return Err(err);
    }
    unsafe {
        CloseHandle(child.hThread);
        WaitForSingleObject(child.hProcess, INFINITE);
    }
    let mut code = 0;
    if unsafe { GetExitCodeProcess(child.hProcess, &mut code) } == 0 {
        return Err(last_error("GetExitCodeProcess"));
    }
    // The job's memory limit makes an allocation fail and the child abort;
    // report it as monty-alloc's OOM exit so the gateway maps both alike.
    Ok(if hit_memory_limit(port) { monty_types::OOM_EXIT_CODE as u32 } else { code })
}

fn memory_cap() -> Result<usize, String> {
    let Some(arg) = std::env::args().nth(1) else { return Ok(DEFAULT_MEMORY_CAP_BYTES) };
    match arg.strip_prefix(MEMORY_CAP_FLAG) {
        Some(value) => value.parse().map_err(|_| format!("bad argument {arg:?}")),
        None => Ok(DEFAULT_MEMORY_CAP_BYTES),
    }
}

/// userenv.dll is loaded at run time, never imported: the confined child runs
/// this same binary, and each DLL it imports must load under its mitigations.
fn load_userenv() -> Result<HMODULE, String> {
    let userenv = unsafe { LoadLibraryExW(wide("userenv.dll").as_ptr(), null_mut(), LOAD_LIBRARY_SEARCH_SYSTEM32) };
    if userenv.is_null() {
        return Err(last_error("load userenv.dll"));
    }
    Ok(userenv)
}

/// The gateway starts the launcher with an empty environment (Go adds only
/// SYSTEMROOT). Sets PROFILE_ENV_VARS from the user's default environment
/// block, which never contains the gateway's own variables; the confined
/// child inherits them, with the AppContainer's folders swapped in.
fn set_profile_environment(userenv: HMODULE) -> Result<(), String> {
    let create_block = unsafe {
        std::mem::transmute::<RawProc, CreateEnvironmentBlockFn>(proc_address(userenv, c"CreateEnvironmentBlock")?)
    };
    let mut token: HANDLE = null_mut();
    let access = TOKEN_QUERY | TOKEN_DUPLICATE | TOKEN_IMPERSONATE;
    if unsafe { OpenProcessToken(GetCurrentProcess(), access, &mut token) } == 0 {
        return Err(last_error("OpenProcessToken"));
    }
    let mut block: *mut c_void = null_mut();
    let created = unsafe { create_block(&mut block, token, 0) };
    unsafe { CloseHandle(token) };
    if created == 0 {
        return Err(last_error("CreateEnvironmentBlock"));
    }
    // The block is "NAME=value\0" entries ending with an empty one. It lives
    // until the launcher exits.
    let mut entry = block.cast::<u16>().cast_const();
    loop {
        let len = (0..).take_while(|&i| unsafe { *entry.add(i) } != 0).count();
        if len == 0 {
            return Ok(());
        }
        let text = String::from_utf16_lossy(unsafe { std::slice::from_raw_parts(entry, len) });
        // Skips the "=C:=C:\\" drive entries, whose name starts with '='.
        if let Some((name, value)) = text.split_once('=').filter(|(name, _)| !name.is_empty())
            && PROFILE_ENV_VARS.iter().any(|v| v.eq_ignore_ascii_case(name))
        {
            // SAFETY: the launcher is single-threaded.
            unsafe { std::env::set_var(name, value) };
        }
        entry = unsafe { entry.add(len + 1) };
    }
}

fn app_container_sid(userenv: HMODULE) -> Result<PSID, String> {
    let create = unsafe { std::mem::transmute::<RawProc, CreateProfileFn>(proc_address(userenv, c"CreateAppContainerProfile")?) };
    let derive = unsafe {
        std::mem::transmute::<RawProc, DeriveSidFn>(proc_address(userenv, c"DeriveAppContainerSidFromAppContainerName")?)
    };
    let name = wide(APP_CONTAINER_NAME);
    let mut sid: PSID = null_mut();
    let mutex = unsafe { CreateMutexW(null(), 0, wide(PROFILE_MUTEX_NAME).as_ptr()) };
    if mutex.is_null() {
        return Err(last_error("CreateMutexW"));
    }
    // WAIT_ABANDONED (a launcher died holding it) also grants ownership.
    unsafe { WaitForSingleObject(mutex, INFINITE) };
    // Fails once the profile exists; the SID derived from the name is the
    // same one. The SID lives until the launcher exits.
    let created = unsafe { create(name.as_ptr(), name.as_ptr(), name.as_ptr(), null(), 0, &mut sid) };
    unsafe { ReleaseMutex(mutex) };
    if created >= 0 {
        return Ok(sid);
    }
    let hr = unsafe { derive(name.as_ptr(), &mut sid) };
    if hr < 0 {
        return Err(format!("DeriveAppContainerSidFromAppContainerName: HRESULT {hr:#010x}"));
    }
    Ok(sid)
}

fn proc_address(module: HMODULE, name: &CStr) -> Result<RawProc, String> {
    unsafe { GetProcAddress(module, name.as_ptr().cast()) }.ok_or_else(|| last_error(&name.to_string_lossy()))
}

/// The job and the completion port that receives its messages. Neither handle
/// is inheritable, so when the launcher dies the job closes and kills the child.
fn create_job(memory_limit: usize) -> Result<(HANDLE, HANDLE), String> {
    let job = unsafe { CreateJobObjectW(null(), null()) };
    if job.is_null() {
        return Err(last_error("CreateJobObjectW"));
    }
    let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        | JOB_OBJECT_LIMIT_ACTIVE_PROCESS
        | JOB_OBJECT_LIMIT_PROCESS_MEMORY
        | JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION;
    limits.BasicLimitInformation.ActiveProcessLimit = 1;
    limits.ProcessMemoryLimit = memory_limit;
    set_job_info(job, JobObjectExtendedLimitInformation, &limits)?;
    let port = unsafe { CreateIoCompletionPort(INVALID_HANDLE_VALUE, null_mut(), 0, 1) };
    if port.is_null() {
        return Err(last_error("CreateIoCompletionPort"));
    }
    let association = JOBOBJECT_ASSOCIATE_COMPLETION_PORT { CompletionKey: null_mut(), CompletionPort: port };
    set_job_info(job, JobObjectAssociateCompletionPortInformation, &association)?;
    Ok((job, port))
}

fn set_job_info<T>(job: HANDLE, class: JOBOBJECTINFOCLASS, info: &T) -> Result<(), String> {
    let size = size_of::<T>() as u32;
    if unsafe { SetInformationJobObject(job, class, (info as *const T).cast(), size) } == 0 {
        return Err(last_error(&format!("SetInformationJobObject({class})")));
    }
    Ok(())
}

/// Drains the job's messages up to "no active process"; a memory-limit
/// violation is always posted before it.
fn hit_memory_limit(port: HANDLE) -> bool {
    let (mut message, mut key, mut overlapped) = (0u32, 0usize, null_mut::<OVERLAPPED>());
    let mut hit = false;
    while unsafe { GetQueuedCompletionStatus(port, &mut message, &mut key, &mut overlapped, JOB_MESSAGE_WAIT_MS) } != 0 {
        match message {
            JOB_OBJECT_MSG_PROCESS_MEMORY_LIMIT => hit = true,
            JOB_OBJECT_MSG_ACTIVE_PROCESS_ZERO => break,
            _ => {}
        }
    }
    hit
}

/// Starts this binary again, suspended, inside the AppContainer, with the
/// mitigations on and only the standard handles inherited. It inherits the
/// launcher's environment: only PROFILE_ENV_VARS (set_profile_environment).
fn spawn_suspended(sid: PSID) -> Result<PROCESS_INFORMATION, String> {
    let [stdin, stdout, stderr] = std_handles()?;
    let mut inherited = vec![stdin, stdout, stderr];
    inherited.sort();
    inherited.dedup(); // a handle listed twice is rejected
    let capabilities = SECURITY_CAPABILITIES { AppContainerSid: sid, Capabilities: null_mut(), CapabilityCount: 0, Reserved: 0 };
    let child_policy = PROCESS_CREATION_CHILD_PROCESS_RESTRICTED;
    let mut attributes = AttributeList::new(4)?;
    attributes.set(PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES, &capabilities)?;
    attributes.set(PROC_THREAD_ATTRIBUTE_CHILD_PROCESS_POLICY, &child_policy)?;
    attributes.set(PROC_THREAD_ATTRIBUTE_MITIGATION_POLICY, &MITIGATIONS)?;
    attributes.set(PROC_THREAD_ATTRIBUTE_HANDLE_LIST, inherited.as_slice())?;

    let exe = own_image_path()?;
    let mut command_line = own_command_line();
    // The AppContainer can traverse the system directory, not the caller's.
    let cwd = system_directory()?;
    let mut startup: STARTUPINFOEXW = unsafe { std::mem::zeroed() };
    startup.StartupInfo.cb = size_of::<STARTUPINFOEXW>() as u32;
    startup.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
    (startup.StartupInfo.hStdInput, startup.StartupInfo.hStdOutput, startup.StartupInfo.hStdError) = (stdin, stdout, stderr);
    startup.lpAttributeList = attributes.as_ptr();
    let mut child: PROCESS_INFORMATION = unsafe { std::mem::zeroed() };
    // DETACHED_PROCESS: no console, so the child never spawns a conhost.
    let flags = EXTENDED_STARTUPINFO_PRESENT | CREATE_SUSPENDED | DETACHED_PROCESS;
    let created = unsafe {
        CreateProcessW(
            wide(&exe).as_ptr(),
            command_line.as_mut_ptr(),
            null(),
            null(),
            1,
            flags,
            null(),
            cwd.as_ptr(),
            &startup.StartupInfo,
            &mut child,
        )
    };
    if created != 0 {
        return Ok(child);
    }
    if unsafe { GetLastError() } == ERROR_ACCESS_DENIED {
        let exe = exe.display();
        return Err(format!(
            "CreateProcessW: access denied, most likely because AppContainers cannot read {exe}; \
             grant them read and execute: icacls \"{exe}\" /grant \"*{ALL_APPLICATION_PACKAGES_SID}:(RX)\""
        ));
    }
    Err(last_error("CreateProcessW"))
}

fn std_handles() -> Result<[HANDLE; 3], String> {
    let mut handles = [null_mut(); 3];
    for (slot, id) in handles.iter_mut().zip([STD_INPUT_HANDLE, STD_OUTPUT_HANDLE, STD_ERROR_HANDLE]) {
        let handle = unsafe { GetStdHandle(id) };
        if handle.is_null() || handle == INVALID_HANDLE_VALUE {
            return Err(format!("standard handle {id:#x} is missing"));
        }
        // PROC_THREAD_ATTRIBUTE_HANDLE_LIST only accepts inheritable handles.
        if unsafe { SetHandleInformation(handle, HANDLE_FLAG_INHERIT, HANDLE_FLAG_INHERIT) } == 0 {
            return Err(last_error("SetHandleInformation"));
        }
        *slot = handle;
    }
    Ok(handles)
}

/// GetModuleFileNameW with backslashes only: the gateway may start the
/// launcher through a path with forward slashes, which the image keeps.
fn own_image_path() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| format!("current_exe: {e}"))?;
    let backslashed: Vec<u16> =
        exe.as_os_str().encode_wide().map(|c| if c == u16::from(b'/') { u16::from(b'\\') } else { c }).collect();
    Ok(PathBuf::from(OsString::from_wide(&backslashed)))
}

/// GetCommandLineW, NUL-terminated and writable as CreateProcessW requires.
fn own_command_line() -> Vec<u16> {
    let raw = unsafe { GetCommandLineW() };
    let len = (0..).take_while(|&i| unsafe { *raw.add(i) } != 0).count();
    let mut line = unsafe { std::slice::from_raw_parts(raw, len) }.to_vec();
    line.push(0);
    line
}

fn system_directory() -> Result<Vec<u16>, String> {
    let mut buf = vec![0u16; 512];
    let len = unsafe { GetSystemDirectoryW(buf.as_mut_ptr(), buf.len() as u32) } as usize;
    if len == 0 || len >= buf.len() {
        return Err(last_error("GetSystemDirectoryW"));
    }
    buf.truncate(len + 1);
    Ok(buf)
}

fn is_app_container() -> Result<bool, String> {
    let mut token: HANDLE = null_mut();
    if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) } == 0 {
        return Err(last_error("OpenProcessToken"));
    }
    let (mut is_app_container, mut len) = (0u32, 0u32);
    let size = size_of::<u32>() as u32;
    let ok = unsafe { GetTokenInformation(token, TokenIsAppContainer, (&raw mut is_app_container).cast(), size, &mut len) };
    unsafe { CloseHandle(token) };
    if ok == 0 {
        return Err(last_error("GetTokenInformation(TokenIsAppContainer)"));
    }
    Ok(is_app_container != 0)
}

fn policy_flags(policy: PROCESS_MITIGATION_POLICY) -> Result<u32, String> {
    let mut flags = 0u32;
    if unsafe { GetProcessMitigationPolicy(GetCurrentProcess(), policy, (&raw mut flags).cast(), size_of::<u32>()) } == 0 {
        return Err(last_error(&format!("GetProcessMitigationPolicy({policy})")));
    }
    Ok(flags)
}

/// A PROC_THREAD_ATTRIBUTE_LIST; the borrow keeps every value it points to
/// alive until the list is dropped, after CreateProcessW.
struct AttributeList<'a> {
    buf: Vec<u64>,
    values: PhantomData<&'a ()>,
}

impl<'a> AttributeList<'a> {
    fn new(count: u32) -> Result<Self, String> {
        let mut size = 0;
        // This first call only reports the size, failing by design.
        unsafe { InitializeProcThreadAttributeList(null_mut(), count, 0, &mut size) };
        let mut buf = vec![0u64; size.div_ceil(size_of::<u64>())];
        if unsafe { InitializeProcThreadAttributeList(buf.as_mut_ptr().cast(), count, 0, &mut size) } == 0 {
            return Err(last_error("InitializeProcThreadAttributeList"));
        }
        Ok(Self { buf, values: PhantomData })
    }

    fn as_ptr(&mut self) -> LPPROC_THREAD_ATTRIBUTE_LIST {
        self.buf.as_mut_ptr().cast()
    }

    fn set<T: ?Sized>(&mut self, attribute: u32, value: &'a T) -> Result<(), String> {
        let (ptr, size) = ((value as *const T).cast(), size_of_val(value));
        if unsafe { UpdateProcThreadAttribute(self.as_ptr(), 0, attribute as usize, ptr, size, null_mut(), null()) } == 0 {
            return Err(last_error(&format!("UpdateProcThreadAttribute({attribute:#x})")));
        }
        Ok(())
    }
}

impl Drop for AttributeList<'_> {
    fn drop(&mut self) {
        unsafe { DeleteProcThreadAttributeList(self.as_ptr()) };
    }
}

fn wide(s: impl AsRef<OsStr>) -> Vec<u16> {
    s.as_ref().encode_wide().chain([0]).collect()
}

fn last_error(what: &str) -> String {
    format!("{what}: {}", std::io::Error::last_os_error())
}
