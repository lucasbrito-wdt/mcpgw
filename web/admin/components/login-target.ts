/** Only same-origin paths inside the area are followed after login; anything else falls back to the area home. */
export function resolveNextTarget(next: string | null, area: string, origin: string): string {
  const home = `/${area}/`;
  if (!next) return home;
  try {
    const url = new URL(next, origin);
    // Never back to the login page itself: a browser that updates location before navigating nests next=/login/.
    if (url.origin !== origin || !url.pathname.startsWith(home) || url.pathname.startsWith(`${home}login/`)) return home;
    return url.pathname + url.search + url.hash;
  } catch {
    return home;
  }
}
