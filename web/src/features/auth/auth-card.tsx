export const authInputClass = `w-full h-9 rounded-md bg-bg-primary border border-border px-3 text-sm
  text-text-primary placeholder:text-text-tertiary
  focus:outline-none focus:border-border-active focus:ring-1 focus:ring-border-active
  transition-colors`;

export function AuthCard({
  title,
  subtitle,
  children,
  footer,
}: {
  title: string;
  subtitle: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
}) {
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
          <h1 className="text-xl font-semibold text-text-primary mb-1">{title}</h1>
          <p className="text-sm text-text-secondary mb-6">{subtitle}</p>
          {children}
        </div>
        {footer && <p className="text-center text-sm text-text-secondary mt-5">{footer}</p>}
      </div>
    </div>
  );
}

export function AuthField({
  id,
  label,
  ...input
}: { id: string; label: string } & React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium text-text-secondary uppercase tracking-wider">
        {label}
      </label>
      <input id={id} required className={authInputClass} {...input} />
    </div>
  );
}

export function AuthMessage({ tone, children }: { tone: "error" | "success"; children: React.ReactNode }) {
  const color = tone === "error" ? "text-danger bg-danger/10 border-danger/20" : "text-accent bg-accent/10 border-accent/20";
  return <p className={`text-sm border rounded-md px-3 py-2 ${color}`}>{children}</p>;
}

export function providerLabel(id: string): string {
  if (id === "github") return "GitHub";
  if (id === "google") return "Google";
  return "SSO";
}
