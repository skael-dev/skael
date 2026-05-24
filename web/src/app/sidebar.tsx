import { useState, useRef, useEffect } from "react";
import { NavLink } from "react-router-dom";
import { Layers, BarChart3, Settings, LogOut } from "lucide-react";
import { useAuth } from "@/app/auth-provider";

type NavItem = {
  id: string;
  label: string;
  path: string;
  icon: React.ReactNode;
  disabled?: boolean;
};

const navItems: NavItem[] = [
  { id: "skills", label: "Skills", path: "/", icon: <Layers size={16} /> },
  {
    id: "analytics",
    label: "Analytics",
    path: "/analytics",
    icon: <BarChart3 size={16} />,
  },
];

const bottomItems: NavItem[] = [
  {
    id: "settings",
    label: "Settings",
    path: "/settings",
    icon: <Settings size={16} />,
  },
];

function Tooltip({ label }: { label: string }) {
  return (
    <div
      className="absolute left-[calc(100%+8px)] top-1/2 -translate-y-1/2 z-50 pointer-events-none
        bg-bg-tertiary border border-border rounded-[5px] px-[9px] py-1
        text-xs text-text-primary whitespace-nowrap shadow-[0_4px_12px_rgba(0,0,0,0.3)]"
    >
      {label}
    </div>
  );
}

function SidebarItem({ item }: { item: NavItem }) {
  const [hovered, setHovered] = useState(false);

  if (item.disabled) {
    return (
      <div
        className="relative w-8 h-8 flex items-center justify-center rounded-md
          text-text-tertiary opacity-40 cursor-not-allowed"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        {item.icon}
        {hovered && <Tooltip label={item.label} />}
      </div>
    );
  }

  return (
    <NavLink
      to={item.path}
      end={item.path === "/"}
      className={({ isActive }) =>
        [
          "relative w-8 h-8 flex items-center justify-center rounded-md transition-colors duration-100",
          isActive
            ? "bg-bg-tertiary text-accent"
            : "text-text-secondary hover:text-text-primary hover:bg-bg-tertiary",
        ].join(" ")
      }
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      {item.icon}
      {hovered && <Tooltip label={item.label} />}
    </NavLink>
  );
}

function UserAvatar() {
  const { user, logout } = useAuth();
  const [open, setOpen] = useState(false);
  const [hovered, setHovered] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // Close dropdown on outside click
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  if (!user) return null;

  const initial = ((user.name ?? user.email)[0] ?? "?").toUpperCase();

  return (
    <div ref={containerRef} className="relative">
      <button
        onClick={() => setOpen((o) => !o)}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        className="relative w-7 h-7 rounded-full bg-accent flex items-center justify-center
          text-[11px] font-semibold font-mono text-bg-primary cursor-pointer
          transition-shadow duration-150 hover:shadow-[0_0_12px_var(--color-accent-surface)]"
        style={{ border: "none" }}
      >
        {initial}
      </button>

      {/* Tooltip (only when dropdown is closed) */}
      {hovered && !open && (
        <div
          className="absolute left-[calc(100%+8px)] top-1/2 -translate-y-1/2 z-50 pointer-events-none
            bg-bg-tertiary border border-border rounded-[5px] px-[9px] py-1.5
            shadow-[0_4px_12px_rgba(0,0,0,0.3)]"
        >
          <div className="text-xs text-text-primary whitespace-nowrap">{user.name}</div>
          <div className="text-[10px] text-text-tertiary whitespace-nowrap">{user.email}</div>
        </div>
      )}

      {/* Dropdown */}
      {open && (
        <div
          className="absolute left-[calc(100%+8px)] bottom-0 z-50
            bg-bg-secondary border border-border rounded-lg shadow-[0_8px_24px_rgba(0,0,0,0.4)]
            min-w-[180px] overflow-hidden"
        >
          <div className="px-3 py-2.5" style={{ borderBottom: "1px solid var(--color-border)" }}>
            <div className="text-[12px] text-text-primary font-medium truncate">{user.name}</div>
            <div className="text-[11px] text-text-tertiary truncate">{user.email}</div>
          </div>
          <button
            onClick={async () => {
              setOpen(false);
              await logout();
            }}
            className="w-full flex items-center gap-2 px-3 py-2 text-[12px] text-text-secondary
              hover:bg-bg-tertiary hover:text-text-primary cursor-pointer transition-colors duration-100 font-sans"
            style={{ border: "none", background: "none" }}
          >
            <LogOut className="size-3.5" />
            Log out
          </button>
        </div>
      )}
    </div>
  );
}

export function Sidebar() {
  return (
    <aside
      className="w-14 min-w-14 h-screen bg-bg-secondary border-r border-border
        flex flex-col items-center shrink-0 relative z-10"
    >
      {/* Logo */}
      <div className="h-11 w-full flex items-center justify-center border-b border-border">
        <div
          className="w-[22px] h-[22px] rounded-md bg-accent flex items-center justify-center
            text-[11px] font-semibold font-mono text-bg-primary
            shadow-[0_0_16px_var(--color-accent-surface)]"
        >
          s
        </div>
      </div>

      {/* Main nav */}
      <nav className="py-3 flex flex-col gap-1">
        {navItems.map((item) => (
          <SidebarItem key={item.id} item={item} />
        ))}
      </nav>

      <div className="flex-1" />

      {/* Bottom items */}
      <div className="pb-3 flex flex-col gap-1.5">
        {bottomItems.map((item) => (
          <SidebarItem key={item.id} item={item} />
        ))}
        <UserAvatar />
      </div>
    </aside>
  );
}
