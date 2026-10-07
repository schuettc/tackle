// coalesce.test.ts — the one-in-flight, one-queued load (coalesce.ts).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { coalesced } from './coalesce.ts';

// A load whose runs the test finishes by hand.
function held() {
  const runs: Array<() => void> = [];
  const load = coalesced(
    () => new Promise<void>((resolve) => runs.push(resolve)),
  );
  return { runs, load };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

test('a burst of calls while one load is in flight queues exactly one more', async () => {
  const { runs, load } = held();
  const first = load();
  const rest = Array.from({ length: 50 }, () => load());
  assert.equal(runs.length, 1, 'one request in flight');
  runs[0]();
  await first;
  await tick();
  assert.equal(runs.length, 2, 'the burst became one queued run');
  runs[1]();
  await Promise.all(rest);
  await tick();
  assert.equal(runs.length, 2, 'nothing more ran');
});

test('every caller resolves after a run that started after its call', async () => {
  const { runs, load } = held();
  void load();
  let done = false;
  const later = load().then(() => {
    done = true;
  });
  runs[0]();
  await tick();
  assert.equal(done, false, 'the first run started before the call');
  runs[1]();
  await later;
  assert.equal(done, true);
});

test('calls after the load settles start a new one', async () => {
  const { runs, load } = held();
  const a = load();
  runs[0]();
  await a;
  void load();
  assert.equal(runs.length, 2);
});

test('a failed run still lets the queued one run', async () => {
  let n = 0;
  const load = coalesced(() => {
    n++;
    return n === 1 ? Promise.reject(new Error('boom')) : Promise.resolve();
  });
  const a = load();
  const b = load();
  await assert.rejects(a);
  await b;
  assert.equal(n, 2);
});
