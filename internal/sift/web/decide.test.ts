import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createDecider, type DecisionServer } from './decide.ts';

// A fake sift serve for an audit round of three files, g and m linked, q
// alone, that decides as the store does (internal/sift/store DecideFile)
// and holds every request until the test serves it (stores it, in the
// order the test picks) and delivers its response (in another order, if
// the test picks one). Synthetic data; no project content.
const groups: Record<string, string[]> = {
  g: ['g', 'm'],
  m: ['g', 'm'],
  q: ['q'],
};

interface Call {
  what: string;
  work: () => unknown;
  out?: { ok: true; v: unknown } | { ok: false; e: unknown };
  resolve(v: unknown): void;
  reject(e: unknown): void;
}

/** A file's decision as the fake stores it. */
interface Dec {
  action: 'accept' | 'edit' | 'reject';
  content?: string;
  note?: string;
  sent?: boolean;
}

const conflict = () =>
  Object.assign(new Error('changed: look again'), { status: 409 });

class Fake {
  recs: Record<string, string> = { g: 'G\n', m: 'M\n', q: 'Q\n' };
  dec: Record<string, Dec | null> = { g: null, m: null, q: null };
  calls: Call[] = [];
  sends = 0;

  print(k: string): string {
    return JSON.stringify(
      groups[k].map((x) => [
        this.recs[x],
        this.dec[x]?.action === 'edit' ? this.dec[x].content : null,
      ]),
    );
  }
  view(k: string): FileView {
    const d = this.dec[k];
    return {
      key: k,
      path: `/w/${k}/AGENTS.md`,
      source: { file: `/w/${k}/AGENTS.md` },
      class: 'repo',
      budget: 6000,
      base: 'b-' + k,
      size: 10,
      after: d?.action === 'edit' ? d.content!.length : this.recs[k].length,
      rows: [],
      rec: {
        file: k,
        base: 'b-' + k,
        content: this.recs[k],
        findings: [],
        summary: 's',
      },
      decision: d ? { ...d } : null,
      fingerprint: this.print(k),
      group: [...groups[k]],
    };
  }
  review(): Review {
    return {
      cursor: '1',
      round: { id: 1, kind: 'on-demand', at: '', summary: {}, owner: '' },
      progress: { state: 'ready', files: 3, recommended: 3 },
      rows: [],
      files: ['g', 'm', 'q'].map((k) => this.view(k)),
      applies: [],
      sends: this.sends,
      home: '',
    };
  }
  check(key: string, prints: Record<string, string>): void {
    for (const k of groups[key])
      if (!prints[k] || prints[k] !== this.print(k)) throw conflict();
  }
  decideFile(key: string, d: FileDecision, prints: Record<string, string>) {
    this.check(key, prints);
    for (const k of groups[key]) {
      const cur = this.dec[k];
      if (k === key && d.action === 'accept' && cur?.action === 'edit')
        this.dec[k] = { ...cur, note: d.note ?? '' };
      else if (k === key) this.dec[k] = { ...d };
      else if (d.action === 'reject')
        this.dec[k] = { action: 'reject', note: cur?.note ?? '' };
      else if (cur && (cur.action === 'edit' || cur.action === 'accept'))
        continue;
      else this.dec[k] = { action: 'accept', note: cur?.note ?? '' };
    }
    return groups[key].map((k) => this.view(k));
  }
  clearFile(key: string, prints: Record<string, string>) {
    this.check(key, prints);
    for (const k of groups[key]) this.dec[k] = null;
    return groups[key].map((k) => this.view(k));
  }

  call<T>(what: string, work: () => T): Promise<T> {
    return new Promise<T>((resolve, reject) =>
      this.calls.push({
        what,
        work,
        resolve,
        reject,
      }),
    );
  }
  /** The server stores call i. */
  serve(i: number): void {
    const c = this.calls[i];
    try {
      c.out = { ok: true, v: c.work() };
    } catch (e) {
      c.out = { ok: false, e };
    }
  }
  /** Call i's response reaches the page. */
  async deliver(i: number): Promise<void> {
    const c = this.calls[i];
    if (c.out?.ok) c.resolve(c.out.v);
    else c.reject(c.out?.e);
    await settle();
  }
  /** Serves and delivers every call not served yet, in order. */
  async run(): Promise<void> {
    for (let i = 0; i < this.calls.length; i++)
      if (!this.calls[i].out) {
        this.serve(i);
        await this.deliver(i);
      }
  }

  server(): DecisionServer {
    return {
      review: () => Promise.resolve(this.review()),
      decideFile: (_r, key, d, prints) =>
        this.call('decide ' + key + ' ' + d.action, () =>
          this.decideFile(key, d, prints),
        ),
      clearFile: (_r, key, prints) =>
        this.call('clear ' + key, () => this.clearFile(key, prints)),
      decide: () => Promise.reject(new Error('no rows here')),
      clear: () => Promise.reject(new Error('no rows here')),
      note: (_r, on, note) =>
        this.call('note ' + (on.file ?? on.id), () => {
          const d = this.dec[on.file!];
          if (!d) throw conflict();
          d.note = note;
          d.sent = false;
        }),
      // As the store's Send: each decision the page showed, only while it
      // is still the one in force and unsent.
      send: (_r, shown) =>
        this.call('send', () => {
          const files: string[] = [];
          for (const [k, w] of Object.entries(shown.files)) {
            const d = this.dec[k];
            if (
              d &&
              !d.sent &&
              d.action === w.action &&
              (d.content ?? '') === (w.content ?? '') &&
              (d.note ?? '') === (w.note ?? '')
            ) {
              d.sent = true;
              files.push(k);
            }
          }
          if (files.length) this.sends++;
          return { sent: files.length, to: '', files, rows: [] };
        }),
    };
  }
}

const settle = () => new Promise((r) => setTimeout(r, 0));

/** A page on the fake: its review, as it shows it, and its decider. */
function page(fake: Fake) {
  const p = { review: fake.review(), failures: [] as string[] };
  const d = createDecider(fake.server(), {
    review: () => p.review,
    replace: (r) => {
      p.review = r;
    },
    changed: () => {},
    failed: (msg) => {
      p.failures.push(msg);
    },
  });
  return { p, d };
}

/** What a page shows of a file, and the print it would send with it. */
function shown(r: Review, k: string) {
  const f = r.files.find((x) => x.key === k);
  return {
    decision: f?.decision?.action ?? null,
    content:
      f?.decision?.action === 'edit' ? f.decision.content : f?.rec?.content,
    fingerprint: f?.fingerprint,
  };
}

test('an edit and a reject on one group, reordered at the server: the page shows the server, and an accept approves what it shows', async () => {
  const fake = new Fake();
  const { p, d } = page(fake);
  const edit = d.file('m', { action: 'edit', content: 'mine\n' });
  const reject = d.file('m', { action: 'reject' });
  // Whatever reached the server, it stores the requests in the reverse of
  // the order they were sent, and the responses arrive reversed too.
  const n = fake.calls.length;
  for (let i = n - 1; i >= 0; i--) fake.serve(i);
  for (let i = n - 1; i >= 0; i--) await fake.deliver(i);
  await Promise.all([edit, reject]);
  for (const k of ['g', 'm'])
    assert.deepEqual(shown(p.review, k), shown(fake.review(), k), k);

  const before = shown(p.review, 'm');
  const accept = d.file('m', { action: 'accept' });
  await fake.run();
  assert.equal(await accept, true, 'the accept was saved');
  const approved = fake.dec.m;
  assert.equal(
    approved?.action === 'edit' ? approved.content : fake.recs.m,
    before.content,
    'the accept approved the content the page showed',
  );
  assert.deepEqual(shown(p.review, 'm'), shown(fake.review(), 'm'));
});

test('while a decision on a group is in flight, its decisions and Send do nothing', async () => {
  const fake = new Fake();
  const { p, d } = page(fake);
  const edit = d.file('m', { action: 'edit', content: 'mine\n' });
  assert.equal(fake.calls.length, 1);
  assert.equal(d.busy(['f:g']), true, 'the linked file is busy too');
  assert.equal(d.busy(['f:q']), false, 'another group is not');
  assert.equal(d.idle(), false);
  // Keys 1, 3 and u on either file of the group, and Send.
  assert.equal(await d.file('g', { action: 'accept' }), false);
  assert.equal(await d.file('m', { action: 'reject' }), false);
  assert.equal(await d.clearFile('m'), false);
  assert.equal(await d.send(), null);
  assert.deepEqual(
    fake.calls.map((c) => c.what),
    ['decide m edit'],
    'nothing else reached the server',
  );
  // Nothing is shown before the server answers.
  assert.equal(shown(p.review, 'm').decision, null);
  // Another group is free.
  const q = d.file('q', { action: 'reject' });
  await fake.run();
  assert.equal(await edit, true);
  assert.equal(await q, true);
  assert.equal(d.idle(), true);
  assert.deepEqual(shown(p.review, 'm'), shown(fake.review(), 'm'));
  assert.equal(shown(p.review, 'm').decision, 'edit');
  const sent = d.send();
  await fake.run();
  assert.deepEqual(await sent, {
    sent: 3,
    to: '',
    files: ['g', 'm', 'q'],
    rows: [],
  });
});

test('a decision on content another page changed gets 409; the page reloads the group, and its next accept approves what it then shows', async () => {
  const fake = new Fake();
  const a = page(fake);
  const edit = a.d.file('m', { action: 'edit', content: 'mine\n' });
  await fake.run();
  assert.equal(await edit, true);
  // Another page, current, takes the edit back and rejects.
  const b = page(fake);
  const clear = b.d.clearFile('m');
  await fake.run();
  assert.equal(await clear, true);
  const reject = b.d.file('m', { action: 'reject' });
  await fake.run();
  assert.equal(await reject, true);
  // The first page still shows its edit; its accept is refused.
  assert.equal(shown(a.p.review, 'm').decision, 'edit');
  const stale = a.d.file('m', { action: 'accept' });
  await fake.run();
  assert.equal(await stale, false);
  await settle();
  assert.equal(fake.dec.m?.action, 'reject', 'nothing approved');
  assert.equal(a.p.failures.length, 1);
  for (const k of ['g', 'm'])
    assert.deepEqual(shown(a.p.review, k), shown(fake.review(), k), k);
  const accept = a.d.file('m', { action: 'accept' });
  await fake.run();
  assert.equal(await accept, true);
  assert.equal(fake.dec.m?.action, 'accept');
  assert.equal(shown(a.p.review, 'm').content, fake.recs.m);
});

test('a stale clear, after the recommendation changed, is refused and installs no print', async () => {
  const fake = new Fake();
  const a = page(fake);
  const edit = a.d.file('m', { action: 'edit', content: 'mine\n' });
  await fake.run();
  await edit;
  // The agent replaces m's recommendation (dropping the group's decisions).
  fake.recs.m = 'M2\n';
  fake.dec = { g: null, m: null, q: fake.dec.q };
  const was = shown(a.p.review, 'm');
  const clear = a.d.clearFile('m');
  await fake.run();
  assert.equal(await clear, false);
  await settle();
  const now = shown(a.p.review, 'm');
  assert.notEqual(now.fingerprint, was.fingerprint);
  assert.deepEqual(now, shown(fake.review(), 'm'), 'it shows the new one');
  const accept = a.d.file('m', { action: 'accept' });
  await fake.run();
  assert.equal(await accept, true);
  assert.equal(fake.dec.m?.action, 'accept');
  assert.equal(shown(a.p.review, 'm').content, 'M2\n');
});

test('a note is saved alone, and one given while the group is busy follows the decision', async () => {
  const fake = new Fake();
  const { p, d } = page(fake);
  d.noteFile('m', 'first');
  assert.equal(fake.calls.length, 0, 'a note on an undecided file waits');
  const rej = d.file('m', { action: 'reject' });
  assert.equal(fake.calls[0].what, 'decide m reject');
  d.noteFile('m', 'second');
  assert.equal(fake.calls.length, 1, 'the note waits for the decision');
  await fake.run();
  await rej;
  await fake.run();
  await settle();
  assert.deepEqual(
    fake.calls.map((c) => c.what),
    ['decide m reject', 'note m'],
  );
  assert.equal(fake.dec.m?.note, 'second');
  assert.equal(
    p.review.files.find((f) => f.key === 'm')?.decision?.note,
    'second',
  );
});

test('rows: a group decision in flight blocks its rows; a clear sends the print shown and shows the row returned', async () => {
  const row = (id: string, d?: Decision): Finding => ({
    id,
    check: 'intake',
    summary: 's',
    source: { file: '', entry: id },
    verdict: 'close:done',
    certain: false,
    // As row.Print: what the page shows, the edit in force over the
    // proposal; not an accept, a reject or a note.
    fingerprint:
      'fp-' +
      id +
      (d?.action === 'edit'
        ? '-edit:' + JSON.stringify([d.verdict, d.title, d.text, d.cleared])
        : ''),
    ...(d ? { decision: d } : {}),
  });
  const review: Review = {
    ...new Fake().review(),
    round: { id: 1, kind: 'backlog', at: '', summary: {}, owner: '' },
    files: [],
    rows: [row('a'), row('b'), row('c', { action: 'reject' })],
  };
  const sent: string[] = [];
  let answer: (v: Finding[]) => void = () => {};
  const server: DecisionServer = {
    ...new Fake().server(),
    decide: (_r, ds) => {
      sent.push(
        'decide ' + ds.map((x) => `${x.id}@${x.fingerprint}`).join(','),
      );
      return new Promise((r) => (answer = r));
    },
    clear: (_r, id, print) => {
      sent.push(`clear ${id}@${print}`);
      return Promise.resolve([row(id)]);
    },
  };
  const d = createDecider(server, {
    review: () => review,
    replace: () => {},
    changed: () => {},
    failed: () => {},
  });
  const group = d.rows([
    { id: 'a', d: { action: 'accept' } },
    { id: 'b', d: { action: 'accept' } },
  ]);
  assert.equal(await d.rows([{ id: 'b', d: { action: 'reject' } }]), false);
  assert.equal(review.rows[0].decision, undefined, 'nothing shown yet');
  answer([row('a', { action: 'accept' }), row('b', { action: 'accept' })]);
  assert.equal(await group, true);
  assert.equal(review.rows[1].decision?.action, 'accept');
  assert.equal(await d.clearRow('c'), true);
  assert.equal(review.rows[2].decision, undefined);
  assert.deepEqual(sent, ['decide a@fp-a,b@fp-b', 'clear c@fp-c']);
});

test('rows: an accept of an edited row answers the edit it shows, and shows the edit the server kept', async () => {
  const edit: Decision = { action: 'edit', text: 'mine' };
  const print = (d?: Decision) =>
    'fp-a' + (d?.action === 'edit' ? '-edit:' + d.text : '');
  const a: Finding = {
    id: 'a',
    check: 'intake',
    summary: 's',
    source: { file: '', entry: 'a' },
    verdict: 'keep',
    text: 'theirs',
    certain: false,
    decision: edit,
    fingerprint: print(edit),
  };
  const review: Review = {
    ...new Fake().review(),
    round: { id: 1, kind: 'backlog', at: '', summary: {}, owner: '' },
    files: [],
    rows: [a],
  };
  const sent: string[] = [];
  const server: DecisionServer = {
    ...new Fake().server(),
    decide: (_r, ds) => {
      sent.push(ds.map((x) => `${x.id} ${x.action}@${x.fingerprint}`).join());
      // The store keeps the edit (shown.Kept).
      const kept: Decision = { ...edit, note: ds[0].note };
      return Promise.resolve([
        { ...a, decision: kept, fingerprint: print(kept) },
      ]);
    },
  };
  const d = createDecider(server, {
    review: () => review,
    replace: () => {},
    changed: () => {},
    failed: () => {},
  });
  assert.equal(await d.rows([{ id: 'a', d: { action: 'accept' } }]), true);
  assert.deepEqual(sent, ['a accept@fp-a-edit:mine']);
  assert.equal(review.rows[0].decision?.action, 'edit');
  assert.equal(review.rows[0].decision?.text, 'mine');
});

test('while a Send is in flight, nothing else runs: no decision, clear, note or second Send', async () => {
  const fake = new Fake();
  const { p, d } = page(fake);
  const q = d.file('q', { action: 'reject' });
  await fake.run();
  assert.equal(await q, true);
  const send = d.send();
  assert.equal(d.idle(), false, 'idle() reports busy');
  assert.equal(d.busy(['f:g']), true);
  assert.equal(d.busy(['f:q']), true);
  assert.equal(await d.file('m', { action: 'edit', content: 'mine\n' }), false);
  assert.equal(await d.file('g', { action: 'accept' }), false);
  assert.equal(await d.clearFile('q'), false);
  assert.equal(await d.send(), null);
  d.noteFile('q', 'later');
  assert.deepEqual(
    fake.calls.map((c) => c.what),
    ['decide q reject', 'send'],
    'nothing else reached the server',
  );
  await fake.run();
  assert.deepEqual(await send, { sent: 1, to: '', files: ['q'], rows: [] });
  // The held note saves once Send lands (run serves it too).
  await fake.run();
  await settle();
  assert.equal(d.idle(), true);
  assert.deepEqual(
    fake.calls.map((c) => c.what),
    ['decide q reject', 'send', 'note q'],
  );
  // The note came after Send was pressed: it is unsent, on the page and in
  // the store.
  assert.equal(fake.dec.q?.sent, false);
  assert.equal(
    p.review.files.find((f) => f.key === 'q')?.decision?.sent,
    false,
  );
});

/** What a page shows as sent, by file. */
const sentOn = (r: Review) =>
  Object.fromEntries(
    r.files.map((f) => [f.key, f.decision ? (f.decision.sent ?? false) : null]),
  );

for (const order of ['edit stored first', 'Send stored first, answered last'])
  test(`Send marks only what it sent (${order}): an edit made after Send was pressed stays unsent, on the page and in the store`, async () => {
    const fake = new Fake();
    const a = page(fake);
    const g = a.d.file('g', { action: 'accept' });
    const q = a.d.file('q', { action: 'reject' });
    await fake.run();
    assert.equal((await g) && (await q), true);
    // Another page, current, is open on the same round.
    const b = page(fake);
    // a presses Send; then, on b, the person edits m.
    const send = a.d.send();
    const edit = b.d.file('m', { action: 'edit', content: 'mine\n' });
    assert.deepEqual(
      fake.calls.slice(-2).map((c) => c.what),
      ['send', 'decide m edit'],
    );
    const [si, ei] = [fake.calls.length - 2, fake.calls.length - 1];
    if (order === 'edit stored first') {
      fake.serve(ei);
      fake.serve(si);
      await fake.deliver(ei);
      await fake.deliver(si);
    } else {
      fake.serve(si);
      fake.serve(ei);
      await fake.deliver(ei);
      await fake.deliver(si);
    }
    assert.equal(await edit, true);
    const out = await send;
    assert.equal(fake.dec.m?.action, 'edit');
    assert.equal(
      fake.dec.m?.sent ?? false,
      false,
      'the store: the edit is unsent',
    );
    assert.equal(shown(b.p.review, 'm').decision, 'edit', 'b shows its edit');
    assert.equal(sentOn(b.p.review).m, false, 'b: the edit is unsent');
    // a marks exactly what the server says it sent, and only that.
    for (const k of ['g', 'm', 'q'])
      assert.equal(
        sentOn(a.p.review)[k],
        out?.files.includes(k) ?? false,
        `a: ${k}`,
      );
    assert.equal(sentOn(a.p.review).q, true);
    if (order === 'edit stored first')
      assert.deepEqual(
        out?.files,
        ['g', 'q'],
        'the accept a showed on m was replaced first',
      );
    else assert.deepEqual(out?.files, ['g', 'm', 'q']);
    // Once a reloads, it shows the edit, unsent.
    const r = await fake.server().review();
    assert.equal(sentOn(r).m, false);
  });

for (const order of ['edit stored first', 'Send stored first, answered last'])
  test(`one page: an edit pressed after Send (${order}) is not marked sent, on the page or in the store`, async () => {
    const fake = new Fake();
    const { p, d } = page(fake);
    const g = d.file('g', { action: 'accept' });
    const q = d.file('q', { action: 'reject' });
    await fake.run();
    assert.equal((await g) && (await q), true);
    const n = fake.calls.length;
    const send = d.send();
    const edit = d.file('m', { action: 'edit', content: 'mine\\n' });
    // Whatever reached the server, in the order picked.
    const si = n;
    const ei = fake.calls.length > n + 1 ? n + 1 : -1;
    if (ei >= 0 && order === 'edit stored first') {
      fake.serve(ei);
      fake.serve(si);
    } else {
      fake.serve(si);
      if (ei >= 0) fake.serve(ei);
    }
    if (ei >= 0) await fake.deliver(ei);
    await fake.deliver(si);
    await edit;
    await send;
    await fake.run();
    await settle();
    const edited = fake.dec.m?.action === 'edit';
    if (edited)
      assert.equal(
        fake.dec.m?.sent ?? false,
        false,
        'the store: the edit is unsent',
      );
    const m = p.review.files.find((f) => f.key === 'm')?.decision;
    if (m?.action === 'edit')
      assert.equal(m.sent ?? false, false, 'the page: the edit is unsent');
    assert.equal(
      edited,
      false,
      'the edit did not run while Send was in flight',
    );
  });
