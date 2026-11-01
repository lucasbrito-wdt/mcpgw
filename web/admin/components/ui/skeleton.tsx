import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

/** Placeholder block while data loads. Size it with className (h-*, w-*); it is hidden from screen readers. */
export function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      aria-hidden
      data-slot="skeleton"
      className={cn("animate-pulse rounded-control bg-white/6 motion-reduce:animate-none", className)}
      {...props}
    />
  );
}
