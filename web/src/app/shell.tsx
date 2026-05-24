import { Outlet } from "react-router-dom";
import { Sidebar } from "@/app/sidebar";
import { TopBar } from "@/app/top-bar";

type ShellProps = {
  onOpenCommand?: () => void;
};

export function Shell({ onOpenCommand }: ShellProps) {
  return (
    <div className="flex h-screen bg-bg-primary overflow-hidden">
      <Sidebar />
      <div className="flex flex-col flex-1 min-w-0">
        <TopBar onOpenCommand={onOpenCommand} />
        <main className="flex-1 overflow-auto">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
