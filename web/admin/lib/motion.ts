/** Signature curve, mirror of --ease-switch in globals.css. Use it for every motion transition. */
export const EASE_SWITCH = [0.22, 1, 0.36, 1] as const;

/** Durations in seconds: state changes, entrances, and the switchboard intro beats. */
export const DURATION = { state: 0.18, enter: 0.32, draw: 0.9 } as const;

/** Default transition for UI motion (tabs, dialogs, swaps). */
export const SWITCH_TRANSITION = { duration: DURATION.enter, ease: EASE_SWITCH };
