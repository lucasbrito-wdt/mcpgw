const SEPARATORS = /[_-]/;

/**
 * Brand key for a server: exact slug, then the slug's first segment (cloudflare_acme -> cloudflare),
 * then any host label of the server URL (mcp.cloudflare.com -> cloudflare). Undefined when nothing matches.
 */
export function resolveBrandKey(slug: string, url: string | undefined, has: (key: string) => boolean): string | undefined {
  const key = slug.toLowerCase();
  if (has(key)) return key;
  const first = key.split(SEPARATORS)[0];
  if (has(first)) return first;
  return hostLabels(url).find(has);
}

function hostLabels(url: string | undefined): string[] {
  if (!url) return [];
  try {
    return new URL(url.includes("://") ? url : `https://${url}`).hostname.toLowerCase().split(".");
  } catch {
    return [];
  }
}
