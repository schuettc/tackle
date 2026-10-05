// api.ts — the page's calls to sift serve's /api/, typed.

import { createApi, ApiError, type Api } from '/_kit/kit.js';
import type { DecisionServer, Sent } from './decide.ts';

export { ApiError };

export interface DecisionIn {
  id: string;
  action: 'accept' | 'edit' | 'reject';
  verdict?: string;
  title?: string;
  text?: string;
  cleared?: ('title' | 'text')[];
  note?: string;
  /** The row's fingerprint as the page shows it, and the id of the
   * decision it shows in force ('' for none): either changed is refused. */
  fingerprint: string;
  decision_id: string;
  /** For an edit to merge:C, C's fingerprint as the page shows it. */
  target_fingerprint?: string;
}

export interface FileOut {
  file: string;
  ref: string;
  content: string;
  truncated: boolean;
}

export interface Client extends DecisionServer {
  /** A file's content at the audit. */
  base(round: number, file: string): Promise<{ content: string }>;
  file(round: number, id: string): Promise<FileOut>;
}

/** The client over a kit Api (base /api; the page's cookie authenticates).
 * Every decision, clear and Send names what the page showed: each item's
 * print and the id of its decision. A decision or clear returns the server's snapshot of what it touched:
 * the group's files, or the rows, each with the print its next decision
 * answers. */
export function client(api: Api): Client {
  return {
    review: () => api.get<Review>('/review'),
    async decide(round, decisions) {
      const r = await api.put<{ rows?: Finding[] }>('/decisions', {
        round,
        decisions,
      });
      return r?.rows ?? [];
    },
    async clear(round, id, fingerprint, decisionID) {
      const q = new URLSearchParams({
        round: String(round),
        id,
        fingerprint,
        decision_id: decisionID,
      });
      const r = await api.del<{ rows?: Finding[] }>(
        `/decisions?${q.toString()}`,
      );
      return r?.rows ?? [];
    },
    async decideFile(round, file, d, seen) {
      const r = await api.put<{ files?: FileView[] }>('/files', {
        round,
        file,
        action: d.action,
        content: d.content ?? '',
        note: d.note ?? '',
        prints: seen.prints,
        decisions: seen.decisions,
      });
      return r?.files ?? [];
    },
    async clearFile(round, file, seen) {
      const r = await api.post<{ files?: FileView[] }>('/files/clear', {
        round,
        file,
        prints: seen.prints,
        decisions: seen.decisions,
      });
      return r?.files ?? [];
    },
    async note(round, on, note) {
      await api.put('/notes', { round, ...on, note });
    },
    base: (round, file) =>
      api.get<{ content: string }>('/base', { round: String(round), file }),
    async send(round, shown) {
      const r = await api.post<Partial<Sent>>('/send', { round, ...shown });
      return {
        sent: r?.sent ?? 0,
        to: r?.to ?? '',
        files: r?.files ?? [],
        rows: r?.rows ?? [],
      };
    },
    file: (round, id) =>
      api.get<FileOut>('/file', { round: String(round), id }),
  };
}

export function newApi(opts: Parameters<typeof createApi>[0]): Api {
  return createApi(opts);
}
