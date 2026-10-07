// sessions.ts — which agent session the dock belongs to, and how a session
// is named.
//
// The page belongs to the session that opened it (galley's rule: an editor
// belongs to the one session that opened it, and no other session ever
// adopts it). casebook_open and `casebook serve` run by a session put
// ?session=<id> in the page URL; a reload keeps it. A page opened with no
// session (a plain terminal, a bookmark) is attached to nothing and asks
// Court to choose, except when exactly one eligible session is here. Nothing
// is guessed: no "last used", no "most recently seen".
//
// serve computes which sessions are eligible (live and not a subagent
// worker); the chooser offers only those, sorted by what it shows.

import type { Looking, Session } from './wire.d.ts';

/** folderOf is the session's working folder (the cwd's last part). */
export function folderOf(s: Session): string {
  return s.cwd ? (s.cwd.split('/').filter(Boolean).pop() ?? '') : '';
}

/**
 * sessionTitle is what a session is called: its harness name (pi's session
 * name, which Court sets), else its folder, else its harness, else its id.
 */
export function sessionTitle(s: Session): string {
  return s.name || folderOf(s) || s.harness || s.id;
}

/**
 * sessionMeta is the muted part after the title: "folder · harness" for a
 * named session, the harness alone when the title is already the folder.
 */
export function sessionMeta(s: Session): string {
  const folder = folderOf(s);
  const parts = s.name && folder ? [folder, s.harness] : [s.harness];
  return parts.filter(Boolean).join(' \u00b7 ');
}

/** pickable is what the chooser offers: eligible sessions, sorted by title. */
export function pickable(sessions: Session[]): Session[] {
  return sessions
    .filter((s) => s.eligible)
    .sort((a, b) => {
      const t = sessionTitle(a).localeCompare(sessionTitle(b), undefined, {
        sensitivity: 'base',
      });
      return t !== 0 ? t : a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
    });
}

/**
 * resolveAttachment is the session a page attaches to when it loads: the
 * URL's session, whatever its state (a session that left is still the one
 * this page belongs to; the dock says so); else the only eligible session;
 * else none. auto is true only for the lone-session case.
 */
export function resolveAttachment(
  urlSession: string,
  sessions: Session[],
): { id: string; auto: boolean } {
  if (urlSession) return { id: urlSession, auto: false };
  const ps = pickable(sessions);
  return ps.length === 1
    ? { id: ps[0].id, auto: true }
    : { id: '', auto: false };
}

/**
 * attachedState is how the attached session stands: none attached; here;
 * left (serve stopped hearing from it); unknown (serve has no such session,
 * e.g. pruned).
 */
export function attachedState(
  id: string,
  sessions: Session[],
): 'none' | 'here' | 'left' | 'unknown' {
  if (!id) return 'none';
  const s = sessions.find((x) => x.id === id);
  if (!s) return 'unknown';
  return s.left ? 'left' : 'here';
}

/** urlSession is the session the page URL carries ('' for none). */
export function urlSession(href: string): string {
  return new URL(href).searchParams.get('session') ?? '';
}

/** urlWithSession is href carrying session (removed when ''). */
export function urlWithSession(href: string, session: string): string {
  const u = new URL(href);
  if (session) u.searchParams.set('session', session);
  else u.searchParams.delete('session');
  return u.toString();
}

/**
 * lookingLine is what an item says while a session is looking into it
 * ("‹session› is looking into it"), or '' when none is.
 */
export function lookingLine(l: Looking | null | undefined): string {
  return l ? `${sessionTitle(l.session)} is looking into it` : '';
}
