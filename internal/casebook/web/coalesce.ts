// coalesce.ts — a load that never piles up.
//
// coalesced(load) is load with at most one run in flight and one queued:
// a call while a run is in flight queues one more run (calls made during
// the same run share it), which starts when the run in flight ends. Every
// call resolves once a run that started after the call has finished, so
// what it loaded is at least as new as the call. A burst of live events
// (a "sessions" event per heartbeat once made a page fetch the session
// list ~15,000 times) is two runs, not one per event. The same pattern as
// To apply's flush (apply.ts).

export function coalesced(load: () => Promise<void>): () => Promise<void> {
  let running: Promise<void> | null = null;
  let queued: Promise<void> | null = null;
  function call(): Promise<void> {
    if (!running) {
      running = load().finally(() => {
        running = null;
      });
      return running;
    }
    if (!queued) {
      queued = running
        .catch(() => {})
        .then(() => {
          queued = null;
          return call();
        });
    }
    return queued;
  }
  return call;
}
