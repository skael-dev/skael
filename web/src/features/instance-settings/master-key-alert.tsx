import { useQuery } from "@tanstack/react-query";
import { Link, useLocation } from "react-router-dom";
import { AlertTriangle } from "lucide-react";
import { getMasterKey } from "@/api/sdk.gen";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { MASTER_KEY_KEY } from "./instance-settings";

// Shows owners and admins, on every page, that a master key is lost.
export function MasterKeyAlert() {
  const { user } = useAuth();
  const { pathname } = useLocation();
  const allowed = isPrivileged(user);
  const key = useQuery({
    queryKey: MASTER_KEY_KEY,
    enabled: allowed,
    staleTime: 60_000,
    queryFn: async () => (await getMasterKey()).data ?? null,
  });
  const st = key.data;
  if (!allowed || !st || pathname === "/settings/instance") return null;
  if ((st.missing ?? []).length === 0 && (st.unreadable ?? []).length === 0) return null;
  return (
    <div role="alert" className="flex items-center gap-2 px-5 py-2 border-b border-warning/40 bg-warning/5 text-xs text-text-primary">
      <AlertTriangle size={14} className="text-warning shrink-0" />
      <span>
        {(st.unreadable ?? []).length === 1
          ? "A saved secret does not open with the current master key."
          : `${(st.unreadable ?? []).length} saved secrets do not open with the current master key.`}
      </span>
      <Link to="/settings/instance" className="text-accent hover:underline">
        Open instance settings
      </Link>
    </div>
  );
}
