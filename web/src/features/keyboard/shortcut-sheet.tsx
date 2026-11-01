import { isPrivileged, useAuth } from "@/app/auth-provider";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { NAV_CHORDS } from "./keys";

function Keys({ keys }: { keys: string[] }) {
  return (
    <span className="flex items-center gap-1 shrink-0">
      {keys.map((k) => (
        <kbd
          key={k}
          className="font-mono text-[11px] text-text-secondary min-w-5 text-center px-[5px] py-px border border-border rounded-[3px] bg-bg-tertiary"
        >
          {k}
        </kbd>
      ))}
    </span>
  );
}

function Group({ title, rows }: { title: string; rows: { keys: string[]; label: string }[] }) {
  return (
    <div>
      <h3 className="text-[10px] font-mono uppercase tracking-[0.08em] text-text-tertiary mb-1.5">
        {title}
      </h3>
      {rows.map((r) => (
        <div
          key={r.label}
          className="flex items-center justify-between gap-3 py-1.5 border-b border-border last:border-0"
        >
          <span className="text-[13px] text-text-primary">{r.label}</span>
          <Keys keys={r.keys} />
        </div>
      ))}
    </div>
  );
}

export function ShortcutSheet({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { user } = useAuth();
  const privileged = isPrivileged(user);
  const chords = NAV_CHORDS.filter((c) => !c.privileged || privileged);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-sm max-h-[85vh] overflow-y-auto bg-bg-secondary border-border text-text-primary"
        onKeyDown={(e) => {
          if (e.key === "?") onOpenChange(false);
        }}
      >
        <DialogHeader className="text-left">
          <DialogTitle className="text-text-primary">Keyboard shortcuts</DialogTitle>
          <DialogDescription className="text-text-secondary">
            Shortcuts do not run while you type in a field.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <Group
            title="General"
            rows={[
              { keys: ["?"], label: "Show this sheet" },
              { keys: ["⌘", "K"], label: "Open the command palette" },
              { keys: ["/"], label: "Filter skills" },
              { keys: ["Esc"], label: "Leave a field or close a dialog" },
            ]}
          />
          <Group
            title="Go to"
            rows={chords.map((c) => ({ keys: ["g", c.key], label: c.label }))}
          />
          <Group
            title="Lists"
            rows={[
              { keys: ["j"], label: "Next row" },
              { keys: ["k"], label: "Previous row" },
              { keys: ["Enter"], label: "Open the row" },
            ]}
          />
        </div>
      </DialogContent>
    </Dialog>
  );
}
