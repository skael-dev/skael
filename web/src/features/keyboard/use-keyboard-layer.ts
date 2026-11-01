import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { createChordTracker, isOverlayOpen, isTypingTarget, NAV_CHORDS } from "./keys";
import { moveListCursor, openActiveListRow } from "./use-list-navigation";

type LayerActions = {
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
  openShortcuts: () => void;
};

export function useKeyboardLayer(actions: LayerActions) {
  const navigate = useNavigate();
  const { user } = useAuth();
  const privileged = isPrivileged(user);
  const latest = useRef({ actions, navigate, privileged });
  latest.current = { actions, navigate, privileged };

  useEffect(() => {
    const tracker = createChordTracker(NAV_CHORDS);

    const handler = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing) return;
      const { actions, navigate, privileged } = latest.current;
      const modified = e.metaKey || e.ctrlKey;

      if (modified && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
        if (actions.paletteOpen) actions.setPaletteOpen(false);
        else if (!isOverlayOpen()) actions.setPaletteOpen(true);
        e.preventDefault();
        return;
      }
      if (modified || e.altKey) return;

      if (isOverlayOpen()) {
        tracker.reset();
        return;
      }
      if (isTypingTarget(e.target)) {
        tracker.reset();
        // Esc leaves a filter field, so the shortcuts work again.
        if (e.key === "Escape" && e.target instanceof HTMLElement) e.target.blur();
        return;
      }

      const result = tracker.press(e.key);
      if (result.kind === "started") return;
      if (result.kind === "matched") {
        if (result.chord.privileged && !privileged) return;
        e.preventDefault();
        navigate(result.chord.path);
        return;
      }

      if (e.key === "?") {
        e.preventDefault();
        actions.openShortcuts();
      } else if (e.key === "j" || e.key === "k") {
        if (moveListCursor(e.key === "j" ? 1 : -1)) e.preventDefault();
      } else if (e.key === "Enter") {
        if (openActiveListRow(e.target)) e.preventDefault();
      }
    };

    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);
}
