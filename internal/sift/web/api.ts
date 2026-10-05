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
  undo(
    round: number,
    r: Pick<Finding, 'id' | 'fingerprint'>,
    note: string,
  ): Promise<void>;
  redo(
    round: number,
    r: Pick<Finding, 'id' | 'fingerprint'>,
    note: string,
  ): Promise<void>;
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
    async undo(round, r, note) {
      await api.post('/undo', {
        round,
        id: r.id,
        note,
        fingerprint: r.fingerprint,
      });
    },
    async redo(round, r, note) {
      await api.post('/redo', {
        round,
        id: r.id,
        note,
        fingerprint: r.fingerprint,
      });
    },
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
