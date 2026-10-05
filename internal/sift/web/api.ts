// api.ts — the page's calls to sift serve's /api/, typed.

import { createApi, ApiError, type Api } from '/_kit/kit.js';

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

export interface Client {
  review(): Promise<Review>;
  decide(round: number, ds: DecisionIn[]): Promise<void>;
  clear(round: number, id: string): Promise<void>;
  /** A decision on an audit round's file (and the files linked to it),
   * with the prints of every file in its group as the page shows them
   * (an edit in place of a recommendation counts). Returns the group's
   * prints after it, for the page's next decision. */
  decideFile(
    round: number,
    file: string,
    d: FileDecision,
    prints: Record<string, string>,
  ): Promise<Record<string, string>>;
  /** Clears a file's group; returns its prints after. */
  clearFile(round: number, file: string): Promise<Record<string, string>>;
  /** A file's content at the audit. */
  base(round: number, file: string): Promise<{ content: string }>;
  send(round: number): Promise<{ sent: number; to: string }>;
  file(round: number, id: string): Promise<FileOut>;
}

/** The client over a kit Api (base /api; the page's cookie authenticates). */
export function client(api: Api): Client {
  return {
    review: () => api.get<Review>('/review'),
    async decide(round, decisions) {
      await api.put('/decisions', { round, decisions });
    },
    async clear(round, id) {
      const q = new URLSearchParams({ round: String(round), id });
      await api.del(`/decisions?${q.toString()}`);
    },
    async decideFile(round, file, d, prints) {
      const r = await api.put<{ prints?: Record<string, string> }>('/files', {
        round,
        file,
        action: d.action,
        content: d.content ?? '',
        note: d.note ?? '',
        prints,
      });
      return r?.prints ?? {};
    },
    async clearFile(round, file) {
      const q = new URLSearchParams({ round: String(round), file });
      const r = await api.del<{ prints?: Record<string, string> }>(
        `/files?${q.toString()}`,
      );
      return r?.prints ?? {};
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
