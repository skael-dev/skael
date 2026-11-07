import { useState, useCallback } from "react";
import { Outlet } from "react-router-dom";
import { Sidebar } from "@/app/sidebar";
import { TopBar } from "@/app/top-bar";
import { MasterKeyAlert } from "@/features/instance-settings/master-key-alert";
import { CommandPalette } from "@/components/command-palette";
import { ShortcutSheet } from "@/features/keyboard/shortcut-sheet";
import { useKeyboardLayer } from "@/features/keyboard/use-keyboard-layer";

export function Shell() {
  const [commandOpen, setCommandOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);

  const openCommand = useCallback(() => setCommandOpen(true), []);
  const closeCommand = useCallback(() => setCommandOpen(false), []);
  const openShortcuts = useCallback(() => setShortcutsOpen(true), []);

  useKeyboardLayer({
    paletteOpen: commandOpen,
    setPaletteOpen: setCommandOpen,
    openShortcuts,
  });

  return (
    <div className="flex h-screen bg-bg-primary overflow-hidden">
      <Sidebar />
      <div className="flex flex-col flex-1 min-w-0">
        <TopBar onOpenCommand={openCommand} onOpenShortcuts={openShortcuts} />
        <main className="flex-1 overflow-y-auto">
          <MasterKeyAlert />
          <Outlet />
        </main>
      </div>
      <CommandPalette open={commandOpen} onClose={closeCommand} onShowShortcuts={openShortcuts} />
      <ShortcutSheet open={shortcutsOpen} onOpenChange={setShortcutsOpen} />
    </div>
  );
}
