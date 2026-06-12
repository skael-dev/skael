import { Link } from "react-router-dom";

export function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] gap-3 text-text-secondary">
      <span className="text-4xl font-semibold text-text-primary">404</span>
      <span className="text-sm text-text-tertiary">Page not found</span>
      <Link to="/" className="text-accent text-sm hover:underline">
        Back to skills
      </Link>
    </div>
  );
}
