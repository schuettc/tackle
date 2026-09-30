// agent.mjs — a fake agent that calls /api/agent/* with X-Local-Token.
// Used by probe.mjs to seed agent state for the dock tests.

/**
 * Create an agent client authenticated with X-Local-Token.
 * @param {string} base  — the serve base URL, e.g. http://127.0.0.1:PORT
 * @param {string} token — the X-Local-Token value from the advert
 */
export function createAgent(base, token) {
  async function call(method, path, body) {
    const resp = await fetch(base + path, {
      method,
      headers: {
        'Content-Type': 'application/json',
        'X-Local-Token': token,
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(`${method} ${path}: ${resp.status} ${text}`);
    }
    const text = await resp.text();
    return text ? JSON.parse(text) : null;
  }

  return {
    /**
     * POST /api/agent/presence
     * @param {string} [harness] — 'pi' (default) or 'claude'
     */
    async presence(session, label, cwd, harness) {
      return call('POST', '/api/agent/presence', {
        id: session,
        harness: harness ?? 'pi',
        label,
        cwd,
        pid: process.pid,
      });
    },

    /** GET /api/agent/status */
    async status(session) {
      return call(
        'GET',
        `/api/agent/status?session=${encodeURIComponent(session)}`,
        undefined,
      );
    },

    /** POST /api/agent/progress */
    async progress(session, text, n, total) {
      return call('POST', '/api/agent/progress', { session, text, n, total });
    },

    /**
     * POST /api/agent/propose — create proposals for items.
     * @param {string} session — the session id
     * @param {string[]} keys — item keys to propose on
     * @param {string} disposition — the proposed disposition
     * @param {string} [note] — optional note
     * @param {string} [until] — optional until value
     */
    async propose(session, keys, disposition, note, until) {
      return call('POST', '/api/agent/propose', {
        session,
        keys,
        disposition,
        ...(note ? { note } : {}),
        ...(until ? { until } : {}),
      });
    },

    /**
     * POST /api/threads — create a new thread for a session.
     * @param {string} session — the session id
     * @param {string} name — the thread name
     */
    async newThread(session, name) {
      return call('POST', '/api/threads', { session, name });
    },

    /**
     * POST /api/messages — post a message to a thread (as Court).
     * @param {number} thread — thread id
     * @param {string} body — message body
     * @param {object} [attached] — optional attached data
     * @param {boolean} [batch] — true drafts it into the thread's batch
     */
    async postMessage(thread, body, attached, batch) {
      return call('POST', '/api/messages', {
        thread,
        body,
        attached: attached ?? {},
        batch: batch ?? false,
      });
    },

    /**
     * GET /api/messages — a thread's messages, its draft batch and drafts.
     * @param {number} thread
     */
    async messages(thread) {
      return call('GET', `/api/messages?thread=${thread}`, undefined);
    },

    /**
     * GET /api/agent/wait — long-poll to pick up a delivery (with short timeout).
     * @param {string} session
     * @returns {object|null} the delivery view or null (204)
     */
    async wait(session) {
      const resp = await fetch(
        `${base}/api/agent/wait?session=${encodeURIComponent(session)}&timeout=3`,
        {
          headers: { 'X-Local-Token': token },
        },
      );
      if (resp.status === 204) return null;
      const text = await resp.text();
      return text ? JSON.parse(text) : null;
    },

    /**
     * POST /api/agent/reply — settle messages.
     * @param {string} session
     * @param {number[]} ids — message ids
     * @param {string} state — 'answered' | 'declined' | 'failed'
     * @param {string} [text]
     * @param {object} [attached] — optional attached data on the reply message
     */
    async reply(session, ids, state, text, attached) {
      return call('POST', '/api/agent/reply', {
        session,
        ids,
        state,
        text: text ?? '',
        ...(attached ? { attached } : {}),
      });
    },

    /**
     * POST /api/agent/settled - settle a delivery turn.
     * @param {string} session
     * @param {number[]} [shown] - delivery ids shown this turn
     */
    async settled(session, shown) {
      return call('POST', '/api/agent/settled', {
        session,
        shown: shown ?? [],
      });
    },

    /**
     * GET /api/session/delivery — get the current in-flight delivery for a session.
     * @param {string} session
     */
    async getDelivery(session) {
      const resp = await fetch(
        `${base}/api/session/delivery?session=${encodeURIComponent(session)}`,
        {
          headers: { 'X-Local-Token': token },
        },
      );
      const text = await resp.text();
      return text ? JSON.parse(text) : null;
    },

    /**
     * POST /api/deliveries/release — release a stuck delivery.
     * @param {number} id — delivery id
     */
    async releaseDelivery(id) {
      return call('POST', '/api/deliveries/release', { id });
    },

    /**
     * POST /api/deliveries/move — move a delivery to another session.
     * @param {number} id — delivery id
     * @param {string} session — target session id
     */
    async moveDelivery(id, session) {
      return call('POST', '/api/deliveries/move', { id, session });
    },

    /**
     * POST /api/sessions/move — move all threads from a left session to another.
     * Used when a left session has queued messages and no in-flight delivery.
     * @param {string} from — source session id
     * @param {string} to — target session id
     */
    async moveSession(from, to) {
      return call('POST', '/api/sessions/move', { session: from, target: to });
    },
  };
}
