import { useRef } from "react";
import { scrollBehavior } from "@/lib/motion";

const ROW_SELECTOR = "[data-list-row]";
const openers = new WeakMap<Element, () => void>();

// j and k walk every row on the page in document order, so a page with two
// lists has one cursor. DOM focus is the cursor: it marks the row for assistive
// technology, and a dialog returns to the row when it closes.
export function moveListCursor(delta: 1 | -1): boolean {
  const rows = Array.from(document.querySelectorAll<HTMLElement>(ROW_SELECTOR));
  if (rows.length === 0) return false;
  const current = document.activeElement?.closest<HTMLElement>(ROW_SELECTOR) ?? null;
  const index = current ? rows.indexOf(current) : -1;
  const next =
    index < 0
      ? delta > 0
        ? 0
        : rows.length - 1
      : Math.min(rows.length - 1, Math.max(0, index + delta));
  const row = rows[next]!;
  row.focus({ preventScroll: true });
  row.scrollIntoView?.({ block: "nearest", behavior: scrollBehavior() });
  return true;
}

// Only the row itself: Enter on a button inside a row stays that button's.
export function openActiveListRow(target: EventTarget | null): boolean {
  if (!(target instanceof Element) || !target.matches(ROW_SELECTOR)) return false;
  const open = openers.get(target);
  if (!open) return false;
  open();
  return true;
}

export type ListRowProps = {
  "data-list-row": string;
  tabIndex: number;
  ref: (el: HTMLElement | null) => void;
};

export function useListNavigation<T>(open: (item: T) => void): (item: T) => ListRowProps {
  const openRef = useRef(open);
  openRef.current = open;
  return (item: T) => ({
    "data-list-row": "",
    tabIndex: -1,
    ref: (el: HTMLElement | null) => {
      if (el) openers.set(el, () => openRef.current(item));
    },
  });
}
