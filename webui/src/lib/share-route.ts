/** Matches the public share landing route, /s/<token>. */
export function matchShareToken(pathname: string): string | null {
  const m = /^\/s\/([A-Za-z0-9_-]+)\/?$/.exec(pathname)
  return m?.[1] ?? null
}
