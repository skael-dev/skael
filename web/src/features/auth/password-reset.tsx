import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { authError, postAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { AuthCard, AuthField, AuthMessage } from "./auth-card";

const submitClass = "w-full h-9 bg-accent text-bg-primary hover:bg-accent/90 font-medium disabled:opacity-50";

export function ForgotPassword() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await postAuth("/password/forgot", { email });
    if (!res.ok) {
      setError((await authError(res, "Could not send the email")).message);
      return;
    }
    setSent(true);
  }

  return (
    <AuthCard
      title="Forgot password"
      subtitle="We send a reset link to your email address"
      footer={<Link to="/login" className="text-accent font-medium">Back to sign in</Link>}
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <AuthField id="email" label="Email" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        {error && <AuthMessage tone="error">{error}</AuthMessage>}
        {sent && <AuthMessage tone="success">If an account exists, a reset link is on its way.</AuthMessage>}
        <Button type="submit" disabled={sent} className={submitClass}>Send reset link</Button>
      </form>
    </AuthCard>
  );
}

export function ResetPassword() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await postAuth("/password/reset", { token: params.get("token") ?? "", password });
    if (!res.ok) {
      setError((await authError(res, "This reset link is not valid")).message);
      return;
    }
    navigate("/login", { replace: true });
  }

  return (
    <AuthCard title="Set a new password" subtitle="Choose a password for your account">
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <AuthField id="new-password" label="New password" type="password" autoComplete="new-password" minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <AuthMessage tone="error">{error}</AuthMessage>}
        <Button type="submit" className={submitClass}>Set password</Button>
      </form>
    </AuthCard>
  );
}
