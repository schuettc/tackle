// key-layer.ts — the page's keyboard layer, over the kit's createKeys.
//
// Pure: no kit or browser imports, so node --test can load it. app.ts gives it
// the kit's createKeys and a reporter (console.error).
//
// The layer is always the shown section's:
//   - its list drives the family's list keys: createKeys({list}) gives j/↓
//     k/↑ o/↵ x ⇧x, and / when the list has a search field. A list whose rows
//     can't be selected (Rules) gets the moving and opening keys only: an x
//     that selects nothing would be a key the ? overlay lists and that does
//     nothing. (The kit has no createKeys({list, select: false}), so those
//     six are registered here, in the family group, with the kit's labels.)
//   - its own keys (Section.keys) are bound.
// Showing another section, or the same section with another list (Attention's
// board has none), rebuilds the kit's registry from nothing: no binding to a
// hidden list or a hidden section's key survives. Page-wide keys (the dock's
// . and ⌘↵, the g a / g r / g p switches) are kept here and bound again into
// every rebuild.
//
// A key that clashes is a programming error. It is reported, loudly, and the
// other keys bind and the section still shows: a clash must be visible, never
// a section that silently fails to appear.

/** The part of the kit's KeyBinding the layer needs. */
export interface KeyLike {
  keys: string;
  label: string;
  run(e: KeyboardEvent): void | boolean;
  group?: string;
  inField?: boolean;
}

/** The kit's ListNav: what the list keys drive. */
export interface NavLike {
  move(d: number): void;
  open(): void;
  toggle(): void;
  toggleRange(): void;
  focusSearch?(): void;
}

/** The kit's Keys. */
export interface KeysLike {
  register(b: KeyLike): () => void;
  showHelp(open?: boolean): void;
  bindings(): Array<{ keys: string; label: string; group: string }>;
  destroy(): void;
}

/** The list a section's keys drive, and whether its rows can be selected. */
export interface ListKeys {
  nav: NavLike;
  selects: boolean;
}

export interface LayerSection {
  id: string;
  /** The section's own keys that work now. */
  keys?(): KeyLike[];
  /** The list the list keys drive now; null when none is shown. */
  listKeys?(): ListKeys | null;
}

export interface KeyLayer {
  /** The page-wide registry (ctx.keys): what is registered on it survives every rebind. */
  keys: KeysLike;
  /** bind makes the layer the section's. app.ts calls it after every show(); nothing changes when nothing did. */
  bind(sec: LayerSection | undefined): void;
}

// The family's list keys the kit binds for createKeys({list}), without x/⇧x.
function navKeys(nav: NavLike): KeyLike[] {
  const ks: KeyLike[] = [
    { keys: 'j', label: 'next', run: () => nav.move(1) },
    { keys: '↓', label: 'next', run: () => nav.move(1) },
    { keys: 'k', label: 'previous', run: () => nav.move(-1) },
    { keys: '↑', label: 'previous', run: () => nav.move(-1) },
    { keys: 'o', label: 'open', run: () => nav.open() },
    { keys: '↵', label: 'open', run: () => nav.open() },
  ];
  if (typeof nav.focusSearch === 'function') {
    const search = nav.focusSearch.bind(nav);
    ks.push({ keys: '/', label: 'search', run: () => search() });
  }
  return ks.map((k) => ({ ...k, group: 'family' }));
}

export function makeKeyLayer(
  create: (list?: NavLike) => KeysLike,
  report: (message: string) => void,
): KeyLayer {
  interface PageKey {
    b: KeyLike;
    unbind: (() => void) | null;
  }
  const page: PageKey[] = [];
  let current = create();
  let shown: LayerSection | undefined;
  let shownList: ListKeys | null = null;
  let shownKeys: KeyLike[] = [];

  const clash = (where: string, b: KeyLike, err: unknown) =>
    report(
      `[casebook] key clash in ${where}: "${b.keys}" (${b.label}): ${err instanceof Error ? err.message : String(err)}`,
    );

  function rebuild(): void {
    current.destroy();
    const l = shownList;
    current = create(l && l.selects ? l.nav : undefined);
    if (l && !l.selects) {
      for (const b of navKeys(l.nav)) {
        try {
          current.register(b);
        } catch (err) {
          clash(`section ${shown?.id} (list)`, b, err);
        }
      }
    }
    for (const p of page) {
      try {
        p.unbind = current.register(p.b);
      } catch (err) {
        p.unbind = null;
        clash('the page', p.b, err);
      }
    }
    for (const b of shownKeys) {
      try {
        current.register(b);
      } catch (err) {
        clash(`section ${shown?.id}`, b, err);
      }
    }
  }

  const same = (a: KeyLike[], b: KeyLike[]) =>
    a.length === b.length && a.every((k, i) => k === b[i]);

  return {
    keys: {
      register(b) {
        const p: PageKey = { b, unbind: current.register(b) }; // a clash throws
        page.push(p);
        return () => {
          p.unbind?.();
          p.unbind = null;
          const i = page.indexOf(p);
          if (i >= 0) page.splice(i, 1);
        };
      },
      showHelp: (open) => current.showHelp(open),
      bindings: () => current.bindings(),
      destroy() {
        page.length = 0;
        current.destroy();
      },
    },
    bind(sec) {
      const l = sec?.listKeys?.() ?? null;
      const ks = sec?.keys?.() ?? [];
      if (
        sec === shown &&
        l?.nav === shownList?.nav &&
        l?.selects === shownList?.selects &&
        same(ks, shownKeys)
      )
        return;
      shown = sec;
      shownList = l;
      shownKeys = ks;
      rebuild();
    },
  };
}
