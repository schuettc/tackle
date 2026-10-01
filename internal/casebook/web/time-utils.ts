// time-utils.ts — pure time-formatting utilities with no kit dependencies.
// Extracted so they can be unit-tested directly (dock.ts imports /_kit/kit.js
// which is not resolvable in Node test mode).

/** Returns the age of a timestamp in milliseconds. */
export function ageMs(ts: string): number {
  return Date.now() - new Date(ts).getTime();
}

/**
 * fmtAge formats a timestamp as a human-readable age string.
 * Under one minute returns "now"; months use "mo" (not "m") to avoid collision
 * with minutes when displayed in uppercase (6m vs 6M ambiguity, spec item 5).
 */
export function fmtAge(ts: string): string {
  const ms = ageMs(ts);
  const s = Math.floor(ms / 1000);
  if (s < 60) return 'now';
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d`;
  const mo = Math.floor(d / 30);
  if (mo < 12) return `${mo}mo`;
  return `${Math.floor(mo / 12)}y`;
}
