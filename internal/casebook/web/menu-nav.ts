// menu-nav.ts — the keyboard for the page's small in-document menus (the
// rule's disposition picker, the conditions' "+ condition").
//
// Their roles (menu, menuitem / menuitemradio) promise what a menu does: the
// arrow keys move between the items (Home and End to the ends), the items
// are one tab stop (a roving tabindex), and the menu closes when focus
// leaves it (Tab out, or a click elsewhere). The opener's own Esc handling
// and click toggle stay the caller's.

/**
 * menuNav wires menu's keyboard: items are the elements matching selector,
 * in document order (re-read on every key, so the items may be redrawn).
 * close runs when focus leaves the menu for anywhere but opener.
 */
export function menuNav(
  menu: HTMLElement,
  selector: string,
  opener: HTMLElement,
  close: () => void,
): void {
  const items = () => [...menu.querySelectorAll<HTMLElement>(selector)];

  // One tab stop: the focused item (or the first) takes the tab order.
  const rove = (to: HTMLElement | undefined) => {
    for (const it of items()) it.tabIndex = it === to ? 0 : -1;
  };

  menu.addEventListener('focusin', (e) => {
    const t = e.target as HTMLElement;
    if (t.matches(selector)) rove(t);
  });

  menu.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.isComposing || e.metaKey || e.ctrlKey || e.altKey) return;
    const list = items();
    if (!list.length) return;
    const at = list.indexOf(document.activeElement as HTMLElement);
    let next = -1;
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowRight':
        next = at < 0 ? 0 : (at + 1) % list.length;
        break;
      case 'ArrowUp':
      case 'ArrowLeft':
        next = at < 0 ? list.length - 1 : (at - 1 + list.length) % list.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = list.length - 1;
        break;
      default:
        return;
    }
    // Handled here: the page's list keys (↑ ↓) must not also move.
    e.preventDefault();
    e.stopPropagation();
    rove(list[next]);
    list[next].focus();
  });

  menu.addEventListener('focusout', (e: FocusEvent) => {
    if (menu.hidden) return;
    const to = e.relatedTarget as Node | null;
    if (to && (menu.contains(to) || to === opener)) return;
    close();
  });
}
