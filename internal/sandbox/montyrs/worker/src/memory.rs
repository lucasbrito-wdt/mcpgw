//! Detects allocation-heavy runs so the gateway can recycle the worker.
//!
//! The system allocator keeps pages it has freed, so a worker that ran one big
//! script stays near its peak RSS forever. On macOS 27 neither
//! malloc_zone_pressure_relief nor waiting gives the pages back (measured), so
//! the only reliable way to return them is to end the process. `PeakAllocator`
//! tracks the peak of the live-byte count that monty-alloc already maintains,
//! and `run_was_heavy` tells the gateway (in "done") to replace this worker.

use std::alloc::{GlobalAlloc, Layout};
use std::sync::atomic::{AtomicUsize, Ordering};

use monty_alloc::LimitedAllocator;
use monty_types::LIVE_MEMORY;

/// A run whose live heap peaked this far above its start is heavy.
const HEAVY_RUN_BYTES: usize = 4 << 20;

static PEAK: AtomicUsize = AtomicUsize::new(0);

/// monty-alloc's allocator plus a peak of its live-byte count.
pub struct PeakAllocator(pub LimitedAllocator);

#[inline]
fn record_peak() {
    let live = LIVE_MEMORY.load(Ordering::Relaxed);
    if live > PEAK.load(Ordering::Relaxed) {
        PEAK.fetch_max(live, Ordering::Relaxed);
    }
}

// SAFETY: every method forwards its arguments unchanged to LimitedAllocator.
unsafe impl GlobalAlloc for PeakAllocator {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        let ptr = unsafe { self.0.alloc(layout) };
        record_peak();
        ptr
    }
    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        unsafe { self.0.dealloc(ptr, layout) }
    }
    unsafe fn alloc_zeroed(&self, layout: Layout) -> *mut u8 {
        let ptr = unsafe { self.0.alloc_zeroed(layout) };
        record_peak();
        ptr
    }
    unsafe fn realloc(&self, ptr: *mut u8, layout: Layout, new_size: usize) -> *mut u8 {
        let new_ptr = unsafe { self.0.realloc(ptr, layout, new_size) };
        record_peak();
        new_ptr
    }
}

/// Starts measuring a run: the peak restarts from what is live now.
pub fn begin_run() {
    PEAK.store(LIVE_MEMORY.load(Ordering::Relaxed), Ordering::Relaxed);
}

/// True when the run just finished peaked past the threshold. Call after the
/// run's heap is dropped.
pub fn run_was_heavy() -> bool {
    PEAK.load(Ordering::Relaxed).saturating_sub(LIVE_MEMORY.load(Ordering::Relaxed)) >= HEAVY_RUN_BYTES
}
