import { useState } from "react";
import { NavLink } from "react-router-dom";
import { Layers, BarChart3, Settings } from "lucide-react";

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
      <div className="pb-3 flex flex-col gap-1">
        {bottomItems.map((item) => (
          <SidebarItem key={item.id} item={item} />
        ))}
      </div>
    </aside>
  );
}
