// Ported from design/v5/src/components/brand/ServiceLogo.tsx on 2026-10-31.
"use client";

import { useCallback } from "react";

import { cn } from "../../lib/utils";

import type { BrandIcon } from "./brand-icon";
import { getBrandIcon } from "./brand-icons";
import { glyphColor, hueFor, monogram } from "./monogram";

export type LogoSize = "xs" | "sm" | "md" | "lg" | "xl";
export type LogoVariant = "tile" | "catalog" | "glyph";

export interface ServiceLogoProps {
  /** Server slug or agent id, looked up in the LobeHub / simple-icons / project registry. */
  slug: string;
  /** Optional server URL: its host (mcp.cloudflare.com) resolves the brand when the slug does not. */
  url?: string;
  /** Display name: drives the monogram and its hue when there is no real logo. */
  name: string;
  /** "tile": dark square from the gateway art. "catalog": light square from the Airbyte catalog. "glyph": mark only. */
  variant?: LogoVariant;
  size?: LogoSize;
  /** Accessible name. Omit when the name is printed next to the logo (decorative, the default). */
  label?: string;
  className?: string;
}

const BOX: Record<LogoSize, string> = {
  xs: "size-5 rounded-[5px]",
  sm: "size-7 rounded-[7px]",
  md: "size-10 rounded-card",
  lg: "size-12 rounded-tile",
  xl: "size-16 rounded-tile",
};

const GLYPH: Record<LogoSize, string> = {
  xs: "size-3",
  sm: "size-4",
  md: "size-[22px]",
  lg: "size-6",
  xl: "size-8",
};

const LETTERS: Record<LogoSize, string> = {
  xs: "text-[9px] tracking-[-0.04em]",
  sm: "text-[11px]",
  md: "text-[15px]",
  lg: "text-[17px]",
  xl: "text-[22px]",
};

/** Monogram colors read --logo-h, written after mount (CSSOM) so no style attribute reaches the HTML. */
const MONOGRAM: Record<Exclude<LogoVariant, "glyph">, string> = {
  tile: "bg-[oklch(0.3_0.06_var(--logo-h,250))] text-[oklch(0.9_0.08_var(--logo-h,250))]",
  catalog: "bg-[oklch(0.92_0.05_var(--logo-h,250))] text-[oklch(0.4_0.11_var(--logo-h,250))]",
};

const SURFACE: Record<Exclude<LogoVariant, "glyph">, string> = {
  tile:
    "bg-linear-to-b from-tile to-tile/50 ring-1 ring-line shadow-[inset_0_1px_0_var(--color-line-strong),inset_0_0_14px_color-mix(in_oklab,var(--color-core)_16%,transparent)]",
  catalog: "bg-fg ring-1 ring-black/5",
};

type GlyphProps = Omit<React.SVGProps<SVGSVGElement>, "fill"> & { icon: BrandIcon; surface: "tile" | "catalog" };

/** Color marks keep their own fills; mono marks are tinted to stay legible on the surface. */
function BrandGlyph({ icon, surface, ...svg }: GlyphProps) {
  const tint = icon.hex ? glyphColor(icon.hex, surface) : undefined;
  return (
    <svg viewBox={icon.viewBox ?? "0 0 24 24"} fill={tint} fillRule={icon.evenodd ? "evenodd" : undefined} {...svg}>
      {icon.paths.map((path) => (
        <path key={path.d} d={path.d} fill={path.fill} />
      ))}
    </svg>
  );
}

/** Real brand mark when LobeHub, simple-icons or the project registry has it, hashed monogram otherwise; on a dark art tile or a light catalog tile. */
export function ServiceLogo({ slug, url, name, variant = "tile", size = "md", label, className }: ServiceLogoProps) {
  const icon = getBrandIcon(slug, url);
  const hue = hueFor(name);
  const setHue = useCallback((el: HTMLElement | null) => el?.style.setProperty("--logo-h", String(hue)), [hue]);
  const a11y = label ? { role: "img" as const, "aria-label": label } : { "aria-hidden": true as const };

  if (variant === "glyph") {
    return icon ? (
      <BrandGlyph icon={icon} surface="tile" className={cn(GLYPH[size], className)} {...a11y} />
    ) : (
      <span className={cn("font-semibold text-fg-muted", LETTERS[size], className)} {...a11y}>
        {monogram(name)}
      </span>
    );
  }

  const surface = variant;
  return (
    <span
      ref={icon ? undefined : setHue}
      className={cn(
        "inline-grid shrink-0 place-items-center overflow-hidden",
        BOX[size],
        icon && SURFACE[surface],
        !icon && ["ring-1 ring-line", MONOGRAM[surface]],
        className,
      )}
      {...a11y}
    >
      {icon ? (
        <BrandGlyph icon={icon} surface={surface} className={GLYPH[size]} aria-hidden />
      ) : (
        <span className={cn("font-bold leading-none", LETTERS[size])}>{monogram(name)}</span>
      )}
    </span>
  );
}
