// section-keys.ts — binds the active section's own keys (Section.keys).
//
// Pure: no kit or browser imports, so node --test can load it. app.ts gives it
// the kit's keys.register and a reporter (console.error).
//
// Only the shown section's keys are registered: showing another section
// unbinds the previous one's first, so two sections can bind the same key.
// A key that still clashes (with a page-wide key, or twice in one section)
// is a programming error. It is reported, loudly, and the section's other
// keys bind and the section still shows: a clash must be visible, never a
// section that silently fails to appear.

/** The part of the kit's KeyBinding the binder needs. */
export interface KeyLike {
  keys: string;
  label: string;
  run(e: KeyboardEvent): void | boolean;
}

export interface KeyedSection {
  id: string;
  keys?: KeyLike[];
}

export function makeKeyBinder(
  register: (b: KeyLike) => () => void,
  report: (message: string) => void,
): (sec: KeyedSection | undefined) => void {
  let bound: KeyedSection | undefined;
  let unbinds: Array<() => void> = [];
  return (sec) => {
    if (sec === bound) return;
    for (const unbind of unbinds) unbind();
    unbinds = [];
    bound = sec;
    for (const b of sec?.keys ?? []) {
      try {
        unbinds.push(register(b));
      } catch (err) {
        const why = err instanceof Error ? err.message : String(err);
        report(
          `[casebook] key clash in section ${sec?.id}: "${b.keys}" (${b.label}): ${why}`,
        );
      }
    }
  };
}
