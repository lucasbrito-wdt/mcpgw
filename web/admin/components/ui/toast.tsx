"use client";

import { CircleAlert, CircleCheck, TriangleAlert, X } from "lucide-react";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { useT } from "../../lib/i18n";
import { ui } from "../../lib/i18n/messages/ui";
import { cn } from "../../lib/utils";

export type ToastTone = "ok" | "warn" | "cut";

export interface ToastOptions {
  tone?: ToastTone;
  /** Milliseconds before it dismisses itself. Defaults to 5000. */
  duration?: number;
}

interface ToastItem {
  id: number;
  message: string;
  tone: ToastTone;
}

const DEFAULT_DURATION = 5000;

const TONE: Record<ToastTone, { icon: typeof CircleCheck; className: string }> = {
  ok: { icon: CircleCheck, className: "text-ok" },
  warn: { icon: TriangleAlert, className: "text-warn" },
  cut: { icon: CircleAlert, className: "text-cut" },
};

type ShowToast = (message: string, options?: ToastOptions) => void;

const ToastContext = createContext<ShowToast | null>(null);

/** `const toast = useToast(); toast("Server saved.", { tone: "ok" })`. Needs a ToastProvider above it. */
export function useToast(): ShowToast {
  const show = useContext(ToastContext);
  if (!show) throw new Error("useToast must be used inside <ToastProvider>");
  return show;
}

/** Mount once near the root. Toasts render in a polite live region (`role="status"`) at the bottom right. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const t = useT(ui);
  const [items, setItems] = useState<ToastItem[]>([]);
  const nextId = useRef(0);
  const timers = useRef(new Map<number, number>());

  const dismiss = useCallback((id: number) => {
    window.clearTimeout(timers.current.get(id));
    timers.current.delete(id);
    setItems((current) => current.filter((item) => item.id !== id));
  }, []);

  const show = useCallback<ShowToast>(
    (message, options = {}) => {
      const id = nextId.current++;
      setItems((current) => [...current, { id, message, tone: options.tone ?? "ok" }]);
      timers.current.set(
        id,
        window.setTimeout(() => dismiss(id), options.duration ?? DEFAULT_DURATION),
      );
    },
    [dismiss],
  );

  useEffect(() => {
    const pending = timers.current;
    return () => pending.forEach((timer) => window.clearTimeout(timer));
  }, []);

  const value = useMemo(() => show, [show]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed right-4 bottom-4 z-60 flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-2"
      >
        {items.map((item) => {
          const { icon: Icon, className } = TONE[item.tone];
          return (
            <div
              key={item.id}
              className="pointer-events-auto flex items-start gap-3 rounded-card border border-line-strong bg-panel px-4 py-3 text-sm text-fg shadow-lg transition-[opacity,translate] duration-300 ease-switch starting:translate-y-2 starting:opacity-0 motion-reduce:transition-none"
            >
              <Icon className={cn("mt-0.5 size-4 shrink-0", className)} aria-hidden />
              <p className="min-w-0 flex-1 leading-5 text-pretty">{item.message}</p>
              <button
                type="button"
                onClick={() => dismiss(item.id)}
                aria-label={t("dismissNotice")}
                className="focus-ring -m-1 grid size-6 cursor-pointer place-items-center rounded-[4px] text-fg-muted hover:bg-white/8 hover:text-fg"
              >
                <X className="size-3.5" aria-hidden />
              </button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}
