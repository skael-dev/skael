export type NavChord = {
  key: string;
  path: string;
  label: string;
  privileged?: boolean;
};

export const NAV_CHORDS: NavChord[] = [
  { key: "s", path: "/", label: "Skills" },
  { key: "r", path: "/review", label: "Review" },
  { key: "a", path: "/analytics", label: "Analytics" },
  { key: "t", path: "/sets", label: "Sets" },
  { key: "c", path: "/service-accounts", label: "Service accounts", privileged: true },
  { key: "w", path: "/webhooks", label: "Webhooks", privileged: true },
  { key: "o", path: "/settings", label: "Settings" },
];

export const CHORD_TIMEOUT_MS = 1000;

const TYPING_SELECTOR = [
  "input",
  "textarea",
  "select",
  "[contenteditable]:not([contenteditable='false'])",
  "[role='textbox']",
  "[role='combobox']",
].join(",");

const OVERLAY_SELECTOR = "[role='dialog'],[role='alertdialog'],[role='menu']";

export function isTypingTarget(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest(TYPING_SELECTOR) !== null;
}

// Radix dialogs and menus and the palette all carry one of these roles, so the
// layer stands down for every overlay without each one having to register.
export function isOverlayOpen(): boolean {
  return document.querySelector(OVERLAY_SELECTOR) !== null;
}

// A plain-key shortcut outside the layer checks this first.
export function shortcutsAllowed(e: KeyboardEvent): boolean {
  return (
    !e.defaultPrevented &&
    !e.isComposing &&
    !e.metaKey &&
    !e.ctrlKey &&
    !e.altKey &&
    !isTypingTarget(e.target) &&
    !isOverlayOpen()
  );
}

export type ChordResult =
  | { kind: "started" }
  | { kind: "matched"; chord: NavChord }
  | { kind: "none" };

// The g prefix expires, so a stray g does not capture a later key.
export function createChordTracker(
  chords: NavChord[],
  timeoutMs = CHORD_TIMEOUT_MS,
  now: () => number = Date.now,
) {
  let startedAt: number | null = null;
  return {
    press(key: string): ChordResult {
      const pending = startedAt !== null && now() - startedAt <= timeoutMs;
      startedAt = null;
      if (pending) {
        const chord = chords.find((c) => c.key === key);
        if (chord) return { kind: "matched", chord };
      }
      if (key === "g") {
        startedAt = now();
        return { kind: "started" };
      }
      return { kind: "none" };
    },
    reset() {
      startedAt = null;
    },
  };
}
