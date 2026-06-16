import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { Loader2 } from "lucide-react";

export function ChangePassword() {
  const { user } = useAuth();
  const navigate = useNavigate();

  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(false);
    setIsSubmitting(true);
    try {
      const res = await fetch("/api/auth/change-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: "Failed to change password" }));
        throw new Error(err.detail || err.error || "Failed to change password");
      }
      setSuccess(true);
      setCurrentPassword("");
      setNewPassword("");
      setTimeout(() => navigate("/", { replace: true }), 1500);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to change password");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div className="min-h-screen bg-bg-primary flex items-center justify-center p-4">
      <div className="w-full max-w-sm animate-fade-up">
        <div className="flex justify-center mb-8">
          <div
            className="w-9 h-9 rounded-lg bg-accent flex items-center justify-center
              text-[15px] font-semibold font-mono text-bg-primary
              shadow-[0_0_24px_var(--color-accent-surface)]"
          >
            s
          </div>
        </div>

        <div className="bg-bg-secondary border border-border rounded-xl p-8 shadow-[0_8px_32px_rgba(0,0,0,0.4)]">
          <h1 className="text-xl font-semibold text-text-primary mb-1">Change Password</h1>
          <p className="text-sm text-text-secondary mb-6">
            {user ? `Signed in as ${user.email}` : "Update your password to continue"}
          </p>

          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="current-password" className="text-xs font-medium text-text-secondary uppercase tracking-wider">
                Current Password
              </label>
              <input
                id="current-password"
                type="password"
                autoComplete="current-password"
                required
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                placeholder="Enter current password"
                className="w-full h-9 rounded-md bg-bg-primary border border-border px-3 text-sm
                  text-text-primary placeholder:text-text-tertiary
                  focus:outline-none focus:border-border-active focus:ring-1 focus:ring-border-active
                  transition-colors"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="new-password" className="text-xs font-medium text-text-secondary uppercase tracking-wider">
                New Password
              </label>
              <input
                id="new-password"
                type="password"
                autoComplete="new-password"
                required
                minLength={8}
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                placeholder="Min. 8 characters"
                className="w-full h-9 rounded-md bg-bg-primary border border-border px-3 text-sm
                  text-text-primary placeholder:text-text-tertiary
                  focus:outline-none focus:border-border-active focus:ring-1 focus:ring-border-active
                  transition-colors"
              />
            </div>

            {error && (
              <p className="text-sm text-danger bg-danger/10 border border-danger/20 rounded-md px-3 py-2">
                {error}
              </p>
            )}

            {success && (
              <p className="text-sm text-accent bg-accent/10 border border-accent/20 rounded-md px-3 py-2">
                Password changed successfully. Redirecting...
              </p>
            )}

            <Button
              type="submit"
              disabled={isSubmitting || success}
              className="w-full h-9 bg-accent text-bg-primary hover:bg-accent/90 font-medium
                disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Changing password...
                </>
              ) : (
                "Change Password"
              )}
            </Button>
          </form>
        </div>
      </div>
    </div>
  );
}
