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
    /** POST /api/agent/presence */
    async presence(session, label, cwd) {
      return call('POST', '/api/agent/presence', {
        session,
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
  };
}
