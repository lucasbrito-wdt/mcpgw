"use client";

import { useEffect, useRef } from "react";

import { nextPollDelay } from "../admin/LiveToggle";

/**
 * Calls `poll` every 5 s while `enabled` and the tab is visible; after 3 failures in a row it waits 30 s.
 * Stops when `enabled` turns off or the component unmounts, and resumes when the tab becomes visible again.
 */
export function useLivePolling(enabled: boolean, poll: () => Promise<void>): void {
  const pollRef = useRef(poll);
  useEffect(() => {
    pollRef.current = poll;
  });

  useEffect(() => {
    if (!enabled) return;
    let timer = 0;
    let errors = 0;
    let inFlight = false;
    let stopped = false;

    const isVisible = () => document.visibilityState === "visible";
    const schedule = () => {
      const delay = nextPollDelay({ errors, visible: isVisible() });
      if (delay !== null && !stopped) timer = window.setTimeout(run, delay);
    };
    async function run() {
      if (stopped || inFlight || !isVisible()) return;
      inFlight = true;
      try {
        await pollRef.current();
        errors = 0;
      } catch {
        errors += 1;
      }
      inFlight = false;
      schedule();
    }
    const onVisibility = () => {
      if (!isVisible()) return;
      window.clearTimeout(timer);
      void run();
    };

    schedule();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [enabled]);
}
