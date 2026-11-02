import { createContext, useContext, useState, useEffect, useCallback } from "react";

export interface User {
  id: string;
  email: string;
  name: string;
  role: string;
}

export interface LoginResult {
  mfaToken?: string;
  mustChangePassword?: boolean;
}

interface AuthContextValue {
  user: User | null;
  isLoading: boolean;
  mustChangePassword: boolean;
  login: (email: string, password: string) => Promise<LoginResult>;
  verifyTotp: (code: string, mfaToken?: string) => Promise<LoginResult>;
  signup: (email: string, name: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  passwordChanged: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

// authError reads auth-all's error envelope and Skael's own error shapes.
export async function authError(res: Response, fallback: string): Promise<Error> {
  const body = await res.json().catch(() => null);
  return new Error(body?.error?.message || body?.detail || body?.error || fallback);
}

export async function postAuth(path: string, body?: unknown): Promise<Response> {
  return fetch(`/api/auth${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
}

async function fetchMustChange(): Promise<boolean> {
  const res = await fetch("/api/auth/account");
  if (!res.ok) return false;
  const data = (await res.json()) as { must_change_password?: boolean };
  return data.must_change_password === true;
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [mustChangePassword, setMustChangePassword] = useState(false);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    fetch("/api/auth/session")
      .then((res) => {
        if (!res.ok) throw new Error("not authenticated");
        return res.json() as Promise<{ user: User | null }>;
      })
      .then(async (data) => {
        setUser(data.user);
        if (data.user) setMustChangePassword(await fetchMustChange());
      })
      .catch(() => setUser(null))
      .finally(() => setIsLoading(false));
  }, []);

  const establish = useCallback(async (res: Response, fallback: string): Promise<LoginResult> => {
    if (!res.ok) throw await authError(res, fallback);
    const data = (await res.json()) as { user: User | null; mfaRequired?: boolean; mfaToken?: string };
    if (data.mfaRequired) return { mfaToken: data.mfaToken ?? "" };
    setUser(data.user);
    const must = await fetchMustChange();
    setMustChangePassword(must);
    return { mustChangePassword: must };
  }, []);

  const login = useCallback(
    async (email: string, password: string) =>
      establish(await postAuth("/sign-in/email", { email, password }), "Invalid credentials"),
    [establish],
  );

  const verifyTotp = useCallback(
    async (code: string, mfaToken?: string) =>
      establish(await postAuth("/totp/verify", mfaToken ? { code, mfaToken } : { code }), "Invalid code"),
    [establish],
  );

  const signup = useCallback(
    async (email: string, name: string, password: string) => {
      await establish(await postAuth("/sign-up/email", { email, name, password }), "Signup failed");
    },
    [establish],
  );

  const logout = useCallback(async () => {
    await postAuth("/sign-out");
    setUser(null);
    setMustChangePassword(false);
  }, []);

  const passwordChanged = useCallback(() => setMustChangePassword(false), []);

  return (
    <AuthContext.Provider
      value={{ user, isLoading, mustChangePassword, login, verifyTotp, signup, logout, passwordChanged }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function isPrivileged(user: User | null): boolean {
  return user?.role === "owner" || user?.role === "admin";
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
