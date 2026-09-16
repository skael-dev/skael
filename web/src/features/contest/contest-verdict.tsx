import { Link } from "react-router-dom";
import type { ContestOutput, Pairing } from "@/api/types.gen";

// A verdict reads as a sentence, and the split is always beside it: a reviewer
// who distrusts a composite number reads the tasks instead.
//
// compact is for a list row, where the full "too close to call" explanation
// names the suite and runs to a paragraph. That paragraph is the point on the
// contest's own page and noise in a row.
export function VerdictLine({
  contest,
  compact = false,
}: {
  contest: ContestOutput;
  compact?: boolean;
}) {
  if (contest.status === "running" || contest.status === "pending") {
    return <span className="text-text-secondary">Running…</span>;
  }
  if (contest.status === "failed") {
    return <span className="text-danger">{contest.last_error || "Failed"}</span>;
  }
  if (contest.outcome === "winner") {
    return (
      <span className="text-text-primary">
        <span className="text-accent font-medium">{contest.winner}</span> wins
      </span>
    );
  }
  return (
    <span className="text-text-secondary">
      {compact ? "too close to call" : contest.detail}
    </span>
  );
}

export function Split({ pairing }: { pairing: Pairing }) {
  return (
    <span className="font-mono text-[11px] text-text-tertiary whitespace-nowrap">
      {pairing.a_wins}–{pairing.b_wins} · {pairing.ties} tied · p={pairing.p_value.toFixed(3)}
    </span>
  );
}

export function SuiteNote({ contest }: { contest: ContestOutput }) {
  return (
    <div
      className="text-[11px] text-text-tertiary"
      title="Who chose the tasks. A suite authored by someone who owns a candidate, or derived from one candidate, is disclosed rather than refused."
    >
      {contest.suite_note}
    </div>
  );
}

export function ContestLink({
  contest,
  children,
}: {
  contest: ContestOutput;
  children: React.ReactNode;
}) {
  return (
    <Link to={`/contests/${contest.id}`} className="hover:text-accent transition-colors">
      {children}
    </Link>
  );
}
