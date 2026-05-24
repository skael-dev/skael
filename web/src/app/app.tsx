import { Routes, Route, Navigate } from "react-router-dom";
import { ErrorBoundary } from "react-error-boundary";
import { AuthProvider, useAuth } from "@/app/auth-provider";
import { Shell } from "@/app/shell";
import { Login } from "@/features/auth/login";
import { Signup } from "@/features/auth/signup";
import { SkillList } from "@/features/skills/skill-list";
import { SkillDetail } from "@/features/skills/skill-detail";
import { Analytics } from "@/features/analytics/analytics";
import { Settings } from "@/features/settings/settings";

function ErrorFallback() {
  return (
    <div className="flex h-screen items-center justify-center bg-bg-primary">
      <div className="text-center">
        <h1 className="text-xl font-medium text-text-primary mb-2">Something went wrong</h1>
        <button
          onClick={() => window.location.reload()}
          className="text-accent underline text-sm hover:text-accent/80 transition-colors"
        >
          Reload
        </button>
      </div>
    </div>
  );
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, isLoading } = useAuth();
  if (isLoading) return null;
  if (!user) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

export function App() {
  return (
    <ErrorBoundary FallbackComponent={ErrorFallback}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/signup" element={<Signup />} />
          <Route
            element={
              <RequireAuth>
                <Shell />
              </RequireAuth>
            }
          >
            <Route path="/" element={<SkillList />} />
            <Route path="/skills/:name" element={<SkillDetail />} />
            <Route path="/analytics" element={<Analytics />} />
            <Route path="/settings" element={<Settings />} />
          </Route>
        </Routes>
      </AuthProvider>
    </ErrorBoundary>
  );
}
