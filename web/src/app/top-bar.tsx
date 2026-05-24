import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Search } from "lucide-react";

type Segment = {
  label: string;
  path?: string;
  mono?: boolean;
};

function useBreadcrumbs(): Segment[] {
  const { pathname } = useLocation();

  if (pathname === "/") {
    return [{ label: "Skills" }];
  }

  const parts = pathname.split("/").filter(Boolean);

  if (parts[0] === "skills" && parts[1]) {
    return [
      { label: "Skills", path: "/" },
      { label: parts[1], mono: true },
    ];
  }

  if (parts[0] === "analytics") {
    return [{ label: "Analytics" }];
  }

  if (parts[0] === "settings") {
    return [{ label: "Settings" }];
  }

  return [{ label: parts[0] ?? pathname }];
}

function Slash() {
  return (
    <span className="text-text-tertiary text-sm px-0.5 select-none">/</span>
  );
}

function SyncIndicator() {
  const [now, setNow] = useState(Date.now());
  const syncedAt = useRef(Date.now());

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  const syncAgo = Math.max(0, Math.floor((now - syncedAt.current) / 1000));
  const syncText =
    syncAgo < 5
      ? "just now"
      : syncAgo < 60
        ? `${syncAgo}s ago`
        : `${Math.floor(syncAgo / 60)}m ago`;

  return (
    <div className="flex items-center gap-1.5 px-3 shrink-0">
      <div
        className="w-[7px] h-[7px] rounded-full bg-accent shadow-[0_0_8px_var(--color-accent)]
          animate-pulse"
      />
      <span className="text-[11px] font-mono text-text-tertiary whitespace-nowrap">
        synced {syncText}
      </span>
    </div>
  );
}

type TopBarProps = {
  onOpenCommand?: () => void;
};

export function TopBar({ onOpenCommand }: TopBarProps) {
  const segments = useBreadcrumbs();
  const navigate = useNavigate();

  return (
    <header
      className="h-12 flex items-center shrink-0 px-3 border-b border-border bg-bg-primary relative z-[8]"
      style={{ gap: 0 }}
    >
      {/* Org badge + workspace name */}
      <div className="flex items-center gap-2 px-2 py-1.5 rounded-md shrink-0">
        <div
          className="w-[22px] h-[22px] rounded-[5px] flex items-center justify-center
            text-[11px] font-semibold font-mono text-bg-primary"
          style={{
            background: "linear-gradient(135deg, var(--color-accent), var(--color-accent-muted))",
          }}
        >
          S
        </div>
        <span className="text-[13px] font-medium text-text-primary">skael</span>
      </div>

      {/* Breadcrumbs */}
      {segments.length > 0 && (
        <>
          <Slash />
          {segments.map((seg, i) => (
            <span key={i} className="flex items-center">
              <span
                onClick={seg.path ? () => navigate(seg.path!) : undefined}
                className={[
                  "text-[13px] px-2 py-1 rounded-[5px] whitespace-nowrap transition-colors duration-100",
                  seg.mono ? "font-mono" : "",
                  i === segments.length - 1
                    ? "text-text-primary"
                    : "text-text-secondary",
                  seg.path
                    ? "cursor-pointer hover:bg-bg-secondary"
                    : "cursor-default",
                ].join(" ")}
              >
                {seg.label}
              </span>
              {i < segments.length - 1 && <Slash />}
            </span>
          ))}
        </>
      )}

      <div className="flex-1" />

      {/* Command palette trigger */}
      <button
        onClick={onOpenCommand}
        className="flex items-center gap-2 h-[30px] px-2.5
          bg-bg-secondary border border-border hover:border-border-active
          rounded-md text-[12px] text-text-tertiary cursor-pointer
          transition-colors duration-100 flex-[0_1_240px] min-w-[140px] font-sans"
      >
        <Search size={13} />
        <span className="flex-1 text-left">Search...</span>
        <kbd
          className="font-mono text-[10px] px-[5px] py-px border border-border
            rounded-[3px] bg-bg-tertiary"
        >
          ⌘K
        </kbd>
      </button>

      {/* Sync indicator */}
      <SyncIndicator />
    </header>
  );
}
