// api.ts — the page's calls to sift serve's /api/, typed.

import { createApi, ApiError, type Api } from '/_kit/kit.js';
import type { DecisionServer } from './decide.ts';

export { ApiError };

export interface DecisionIn {
  id: string;
  action: 'accept' | 'edit' | 'reject';
  verdict?: string;
  title?: string;
  text?: string;
  cleared?: ('title' | 'text')[];
  note?: string;
  /** The row's fingerprint as the page shows it: a changed row is refused. */
  fingerprint: string;
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
 * A decision or clear returns the server's snapshot of what it touched:
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
    async clear(round, id, fingerprint) {
      const q = new URLSearchParams({ round: String(round), id, fingerprint });
      const r = await api.del<{ rows?: Finding[] }>(
        `/decisions?${q.toString()}`,
      );
      return r?.rows ?? [];
    },
    async decideFile(round, file, d, prints) {
      const r = await api.put<{ files?: FileView[] }>('/files', {
        round,
        file,
        action: d.action,
        content: d.content ?? '',
        note: d.note ?? '',
        prints,
      });
      return r?.files ?? [];
    },
    async clearFile(round, file, prints) {
      const r = await api.post<{ files?: FileView[] }>('/files/clear', {
        round,
        file,
        prints,
      });
      return r?.files ?? [];
    },
    async note(round, on, note) {
      await api.put('/notes', { round, ...on, note });
    },
    base: (round, file) =>
      api.get<{ content: string }>('/base', { round: String(round), file }),
    async send(round) {
      const r = await api.post<{ sent: number; to?: string }>('/send', {
        round,
      });
      return { sent: r?.sent ?? 0, to: r?.to ?? '' };
    },
    file: (round, id) =>
      api.get<FileOut>('/file', { round: String(round), id }),
  };
}

export function newApi(opts: Parameters<typeof createApi>[0]): Api {
  return createApi(opts);
}
