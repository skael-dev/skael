import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useAuth, type LoginResult } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { Loader2 } from "lucide-react";
import { AuthCard, AuthField, AuthMessage, providerLabel } from "./auth-card";
import { useSignInMethods } from "./use-sign-in-methods";

const oauthErrors: Record<string, string> = {
  EMAIL_ALREADY_EXISTS: "An account with this email already exists. Sign in with your password, then link the provider in Settings.",
  FORBIDDEN: "Signup is not open for this account.",
};

export function Login() {
  const { login, verifyTotp } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const methods = useSignInMethods();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  // A provider sign-in holds its challenge in a cookie, so it has no token here.
  const [mfaToken, setMfaToken] = useState<string | null>(params.get("mfa") === "required" ? "" : null);
  const redirectError = params.get("error");
  const [error, setError] = useState<string | null>(
    redirectError ? (oauthErrors[redirectError] ?? "Sign-in failed. Try again.") : null,
  );
  const [isSubmitting, setIsSubmitting] = useState(false);

  function finish(result: LoginResult) {
    if (result.mfaToken !== undefined) {
      setMfaToken(result.mfaToken);
      return;
    }
    navigate(result.mustChangePassword ? "/change-password" : "/", { replace: true });
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      finish(mfaToken === null ? await login(email, password) : await verifyTotp(code, mfaToken || undefined));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setIsSubmitting(false);
    }
  }

  if (mfaToken !== null) {
    return (
      <AuthCard title="Two-factor code" subtitle="Enter the code from your authenticator app">
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <AuthField
            id="totp-code"
            label="Code"
            inputMode="numeric"
            autoComplete="one-time-code"
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
          {error && <AuthMessage tone="error">{error}</AuthMessage>}
          <Button type="submit" disabled={isSubmitting} className="w-full h-9 bg-accent text-bg-primary hover:bg-accent/90 font-medium disabled:opacity-50">
            Verify
          </Button>
        </form>
      </AuthCard>
    );
  }

  return (
    <AuthCard
      title="Welcome back"
      subtitle="Sign in to your Skael account"
      footer={
        methods?.signup !== false && (
          <>
            Don't have an account?{" "}
            <Link to="/signup" className="text-accent hover:text-accent/80 transition-colors font-medium">
              Sign up
            </Link>
          </>
        )
      }
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <AuthField
          id="email"
          label="Email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="you@example.com"
        />
        <AuthField
          id="password"
          label="Password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="••••••••"
        />
        {methods?.forgot_password && (
          <Link to="/forgot-password" className="text-xs text-text-secondary hover:text-accent transition-colors self-end">
            Forgot password?
          </Link>
        )}
        {error && <AuthMessage tone="error">{error}</AuthMessage>}
        <Button
          type="submit"
          disabled={isSubmitting}
          className="w-full h-9 bg-accent text-bg-primary hover:bg-accent/90 font-medium
            disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        >
          {isSubmitting ? (
            <>
              <Loader2 size={14} className="animate-spin" />
              Signing in...
            </>
          ) : (
            "Sign in"
          )}
        </Button>
      </form>
      {methods && methods.providers.length > 0 && (
        <div className="flex flex-col gap-2 mt-4 pt-4 border-t border-border">
          {methods.providers.map((id) => (
            <a
              key={id}
              href={`/api/auth/oauth/${id}?redirect_to=/`}
              className="h-9 rounded-md border border-border flex items-center justify-center text-sm text-text-primary hover:border-border-active transition-colors"
            >
              Continue with {providerLabel(id)}
            </a>
          ))}
        </div>
      )}
    </AuthCard>
  );
}
