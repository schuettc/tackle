// api.ts — the page's calls to cull serve's /api/, typed.

import { createApi, ApiError, type Api } from '/_kit/kit.js';

export { ApiError };

export interface AnswerIn {
  id: string;
  hash: string;
  kind: 'test' | 'group';
  value: string;
  note: string;
  via: 'item' | 'group';
  blind: boolean;
}

export interface Client {
  review(project: number): Promise<Review>;
  put(project: number, run: number, answers: AnswerIn[]): Promise<void>;
  del(project: number, id: string, hash: string): Promise<void>;
  send(project: number): Promise<{ sent: number; to: string }>;
}

/** The client over a kit Api (base /api; the page's cookie authenticates). */
export function client(api: Api): Client {
  return {
    review: (project) =>
      api.get<Review>('/review', { project: String(project) }),
    async put(project, run, answers) {
      await api.put('/answers', { project, run, answers });
    },
    async del(project, id, hash) {
      const q = new URLSearchParams({ project: String(project), id, hash });
      await api.del(`/answers?${q.toString()}`);
    },
    async send(project) {
      const r = await api.post<{ sent: number; to?: string }>('/send', {
        project,
      });
      return { sent: r?.sent ?? 0, to: r?.to ?? '' };
    },
  };
}

export function newApi(opts: Parameters<typeof createApi>[0]): Api {
  return createApi(opts);
}
