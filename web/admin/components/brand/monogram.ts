// Ported from design/v5/src/components/brand/monogram.ts on 2026-10-31.
/* Fallback identity for services without a real logo. Ported from design/v4/v4-route.js. */

// 270-340 (violet to magenta) is skipped: violet belongs to the gateway, a service must never look like it.
const SKIPPED_FROM = 270;
const SKIPPED_SPAN = 70;

/** Stable hue for a name. FNV-1a spreads short similar names (GitHub, GitLab) far apart. */
export function hueFor(name: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < name.length; i++) hash = Math.imul(hash ^ name.charCodeAt(i), 0x01000193) >>> 0;
  const hue = hash % (360 - SKIPPED_SPAN);
  return hue >= SKIPPED_FROM ? hue + SKIPPED_SPAN : hue;
}

const SMALL_WORDS = /^(e|de|da|do)$/;

/** Two letters: initials of two words, or the first two capitals (GitHub -> Gh). */
export function monogram(name: string): string {
  const words = name
    .replace(/\(.*\)/, "")
    .trim()
    .split(/\s+/)
    .filter((word) => !SMALL_WORDS.test(word));
  if (words.length > 1) return (words[0][0] + words[1][0]).toUpperCase();
  const word = words[0] ?? "?";
  const caps = word.match(/[A-Z]/g);
  if (caps && caps.length > 1) return caps[0] + caps[1].toLowerCase();
  return word[0].toUpperCase() + (word[1] ?? "").toLowerCase();
}

function channel(value: number): number {
  const s = value / 255;
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

function luminance(hex: string): number {
  const n = Number.parseInt(hex, 16);
  return 0.2126 * channel((n >> 16) & 255) + 0.7152 * channel((n >> 8) & 255) + 0.0722 * channel(n & 255);
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

function isNearGray(hex: string): boolean {
  const n = Number.parseInt(hex, 16);
  const rgb = [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  return Math.max(...rgb) - Math.min(...rgb) < 28;
}

const TILE_HEX = "1B1838";
const CATALOG_HEX = "F8F9FC";

/**
 * Glyph color that stays legible on its tile. Brand hex (no #) in; CSS color out.
 * Dark tiles lift dim brand colors toward fg; near-black marks become fg.
 */
export function glyphColor(brandHex: string, surface: "tile" | "catalog"): string {
  if (surface === "catalog") return contrast(brandHex, CATALOG_HEX) < 1.6 ? "var(--color-ink)" : `#${brandHex}`;
  if (contrast(brandHex, TILE_HEX) >= 3) return `#${brandHex}`;
  if (isNearGray(brandHex)) return "var(--color-fg)";
  return `color-mix(in oklab, #${brandHex} 55%, var(--color-fg))`;
}
