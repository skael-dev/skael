import { Routes, Route, Navigate } from "react-router-dom";
import { ContestPage } from "@/features/contest/contest-page";
import { ErrorBoundary } from "react-error-boundary";
import { AuthProvider, useAuth } from "@/app/auth-provider";
import { Shell } from "@/app/shell";
import { Login } from "@/features/auth/login";
import { Signup } from "@/features/auth/signup";
import { ChangePassword } from "@/features/auth/change-password";
import { ForgotPassword, ResetPassword } from "@/features/auth/password-reset";
import { SkillList } from "@/features/skills/skill-list";
import { SkillDetail } from "@/features/skills/skill-detail";
import { Analytics } from "@/features/analytics/analytics";
import { Settings } from "@/features/settings/settings";
import { Ownership } from "@/features/settings/ownership";
import { ReviewQueue } from "@/features/review/review-queue";
import { Quadrant } from "@/features/analytics/quadrant";
import { NotFound } from "@/features/not-found";
import { SetList } from "@/features/sets/set-list";
import { SetDetailPage } from "@/features/sets/set-detail";
import { ServiceAccounts } from "@/features/service-accounts/service-accounts";
import { Webhooks } from "@/features/webhooks/webhooks";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";

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
  const { user, isLoading, mustChangePassword } = useAuth();
  if (isLoading) {
    return (
      <div className="h-screen bg-bg-primary p-6">
        <RowsSkeleton rows={6} />
      </div>
    );
  }
  if (!user) return <Navigate to="/login" replace />;
  if (mustChangePassword) return <Navigate to="/change-password" replace />;
  return <>{children}</>;
}

export function App() {
  return (
    <ErrorBoundary FallbackComponent={ErrorFallback}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/signup" element={<Signup />} />
          <Route path="/change-password" element={<ChangePassword />} />
          <Route path="/forgot-password" element={<ForgotPassword />} />
          <Route path="/reset-password" element={<ResetPassword />} />
          <Route
            element={
              <RequireAuth>
                <Shell />
              </RequireAuth>
            }
          >
            <Route path="/" element={<SkillList />} />
            <Route path="/skills/:name" element={<SkillDetail />} />
            <Route path="/contests/:id" element={<ContestPage />} />
            <Route path="/analytics" element={<Analytics />} />
            <Route path="/review" element={<ReviewQueue />} />
            <Route path="/quadrant" element={<Quadrant />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/settings/ownership" element={<Ownership />} />
            <Route path="/sets" element={<SetList />} />
            <Route path="/sets/:name" element={<SetDetailPage />} />
            <Route path="/service-accounts" element={<ServiceAccounts />} />
            <Route path="/webhooks" element={<Webhooks />} />
          </Route>
          <Route path="*" element={<NotFound />} />
        </Routes>
      </AuthProvider>
    </ErrorBoundary>
  );
}
