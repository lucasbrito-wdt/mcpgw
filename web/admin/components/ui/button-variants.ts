// Ported from design/v5/src/components/ui/button-variants.ts on 2026-10-31.
import { cva } from "class-variance-authority";

/**
 * Airbyte buttons, measured: 12px/600, radius 6. Primary is signal, secondary is the #707089 outline.
 * States: hover, focus-visible ring, active press, disabled, loading (aria-busy, spinner, keeps focus).
 * Use buttonVariants() on a Next Link when the action navigates. This file has no "use client",
 * so Server Components can call it; import it from here, not from ./button.
 */
export const buttonVariants = cva(
  "inline-flex shrink-0 cursor-pointer select-none items-center justify-center gap-1.5 whitespace-nowrap rounded-control border font-semibold outline-none transition-[background-color,border-color,color,box-shadow,transform] duration-150 ease-switch focus-visible:ring-2 focus-visible:ring-signal-fg focus-visible:ring-offset-2 focus-visible:ring-offset-ink active:translate-y-px disabled:pointer-events-none disabled:opacity-45 aria-disabled:pointer-events-none aria-disabled:opacity-45 aria-busy:cursor-progress motion-reduce:transition-none motion-reduce:active:translate-y-0 [&_svg]:pointer-events-none [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        primary:
          "border-transparent bg-signal text-white shadow-[inset_0_1px_0_rgb(255_255_255/0.18)] hover:bg-[color-mix(in_oklab,var(--color-signal)_88%,white)] active:bg-[color-mix(in_oklab,var(--color-signal)_88%,black)]",
        secondary: "border-fg-subtle bg-transparent text-fg hover:border-fg-muted hover:bg-white/5 active:bg-white/8",
        ghost: "border-transparent bg-transparent text-fg-muted hover:bg-white/6 hover:text-fg active:bg-white/10",
        danger: "border-cut/50 bg-cut-wash text-cut hover:border-cut hover:bg-cut/20 active:bg-cut/25",
        link: "h-auto border-transparent px-0 text-signal-fg underline-offset-4 hover:underline",
      },
      size: {
        sm: "h-7 px-2.5 text-xs [&_svg:not([class*='size-'])]:size-3.5",
        md: "h-8 px-3 text-xs [&_svg:not([class*='size-'])]:size-4",
        lg: "h-10 px-4 text-sm [&_svg:not([class*='size-'])]:size-4",
        icon: "size-8 text-xs [&_svg:not([class*='size-'])]:size-4",
        "icon-sm": "size-7 text-xs [&_svg:not([class*='size-'])]:size-3.5",
      },
    },
    defaultVariants: { variant: "secondary", size: "md" },
  },
);
