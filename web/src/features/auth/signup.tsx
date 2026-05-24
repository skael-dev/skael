import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { Loader2 } from "lucide-react";

export function Signup() {
  const { signup } = useAuth();
  const navigate = useNavigate();

  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await signup(email, name, password);
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Signup failed");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div className="min-h-screen bg-bg-primary flex items-center justify-center p-4">
      <div className="w-full max-w-sm animate-fade-up">
        {/* Logo */}
        <div className="flex justify-center mb-8">
          <div
            className="w-9 h-9 rounded-lg bg-accent flex items-center justify-center
              text-[15px] font-semibold font-mono text-bg-primary
              shadow-[0_0_24px_var(--color-accent-surface)]"
          >
            s
          </div>
        </div>

        {/* Card */}
        <div className="bg-bg-secondary border border-border rounded-xl p-8 shadow-[0_8px_32px_rgba(0,0,0,0.4)]">
          <h1 className="text-xl font-semibold text-text-primary mb-1">Create your account</h1>
          <p className="text-sm text-text-secondary mb-6">Get started with Skael</p>

          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="email" className="text-xs font-medium text-text-secondary uppercase tracking-wider">
                Email
              </label>
              <input
                id="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                className="w-full h-9 rounded-md bg-bg-primary border border-border px-3 text-sm
                  text-text-primary placeholder:text-text-tertiary
                  focus:outline-none focus:border-border-active focus:ring-1 focus:ring-border-active
                  transition-colors"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="name" className="text-xs font-medium text-text-secondary uppercase tracking-wider">
                Name
              </label>
              <input
                id="name"
                type="text"
                autoComplete="name"
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Your name"
                className="w-full h-9 rounded-md bg-bg-primary border border-border px-3 text-sm
                  text-text-primary placeholder:text-text-tertiary
                  focus:outline-none focus:border-border-active focus:ring-1 focus:ring-border-active
                  transition-colors"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="password" className="text-xs font-medium text-text-secondary uppercase tracking-wider">
                Password
              </label>
              <input
                id="password"
                type="password"
                autoComplete="new-password"
                required
                minLength={8}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
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

            <Button
              type="submit"
              disabled={isSubmitting}
              className="w-full h-9 bg-accent text-bg-primary hover:bg-accent/90 font-medium
                disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Loader2 size={14} className="animate-spin" />
                  Creating account...
                </>
              ) : (
                "Create account"
              )}
            </Button>
          </form>
        </div>

        <p className="text-center text-sm text-text-secondary mt-5">
          Already have an account?{" "}
          <Link to="/login" className="text-accent hover:text-accent/80 transition-colors font-medium">
            Log in
          </Link>
        </p>
      </div>
    </div>
  );
}
