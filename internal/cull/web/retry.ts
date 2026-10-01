// retry.ts — how long to wait before the nth retry of the first load.

const STEPS = [1000, 2000, 5000];

/** Delay in ms before retry number `attempt` (0-based): 1 s, 2 s, 5 s, then 10 s. */
export function retryDelay(attempt: number): number {
  return STEPS[attempt] ?? 10000;
}
