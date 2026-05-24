import { useState } from "react";
import { ChevronDown, ChevronRight, ShieldAlert, ShieldCheck } from "lucide-react";
import { cn } from "@/lib/utils";

// ── Types (matches Go scan.Report) ───────────────────────────────

export type ScanFinding = {
  rule: string;
  severity: string;
  confidence: string;
  file: string;
  line: number;
  match: string;
  message: string;
};

export type ScanSummary = {
  critical: number;
  high: number;
  medium: number;
  info: number;
};

export type ScanReport = {
  status: string;
  findings: ScanFinding[];
  summary: ScanSummary;
};

// ── Severity config ──────────────────────────────────────────────

type SeverityStyle = { bg: string; text: string; label: string };

const SEVERITY_STYLES: Record<string, SeverityStyle> = {
  critical: { bg: "bg-danger/15", text: "text-danger", label: "Critical" },
  high: { bg: "bg-warning/15", text: "text-warning", label: "High" },
  medium: { bg: "bg-amber-400/15", text: "text-amber-400", label: "Medium" },
  info: { bg: "bg-text-tertiary/15", text: "text-text-tertiary", label: "Info" },
};

const DEFAULT_SEVERITY: SeverityStyle = { bg: "bg-text-tertiary/15", text: "text-text-tertiary", label: "Info" };

function getSeverityConfig(severity: string): SeverityStyle {
  return SEVERITY_STYLES[severity] ?? DEFAULT_SEVERITY;
}

// ── FindingRow ───────────────────────────────────────────────────

function FindingRow({ finding }: { finding: ScanFinding }) {
  const [expanded, setExpanded] = useState(false);
  const sev = getSeverityConfig(finding.severity);

  // Truncate match text for collapsed view
  const truncatedMatch =
    finding.match.length > 60
      ? finding.match.slice(0, 60) + "..."
      : finding.match;

  return (
    <div className="border-b border-border last:border-b-0">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-3 px-4 py-3 text-left bg-transparent border-none cursor-pointer hover:bg-bg-secondary transition-colors duration-100 font-sans"
      >
        {/* Expand icon */}
        {expanded ? (
          <ChevronDown size={14} className="text-text-tertiary shrink-0" />
        ) : (
          <ChevronRight size={14} className="text-text-tertiary shrink-0" />
        )}

        {/* Severity badge */}
        <span
          className={cn(
            "text-[10px] font-medium uppercase tracking-wider px-1.5 py-0.5 rounded shrink-0",
            sev.bg,
            sev.text
          )}
        >
          {sev.label}
        </span>

        {/* Rule name */}
        <span className="text-xs font-mono text-text-primary shrink-0">
          {finding.rule}
        </span>

        {/* File:line */}
        <span className="text-[11px] text-text-tertiary font-mono shrink-0">
          {finding.file}:{finding.line}
        </span>

        {/* Truncated match */}
        <span className="text-[11px] text-text-secondary truncate flex-1 min-w-0">
          {truncatedMatch}
        </span>
      </button>

      {expanded && (
        <div className="px-4 pb-3 pl-[52px]">
          {/* Message */}
          <div className="text-xs text-text-secondary mb-2 leading-relaxed">
            {finding.message}
          </div>

          {/* Full match */}
          <div className="bg-bg-tertiary border border-border rounded px-3 py-2 font-mono text-[11px] text-text-primary whitespace-pre-wrap break-all mb-2">
            {finding.match}
          </div>

          {/* Meta */}
          <div className="flex gap-4 text-[10px] text-text-tertiary">
            <span>Confidence: {finding.confidence}</span>
            <span>
              Location: {finding.file}:{finding.line}
            </span>
          </div>
        </div>
      )}
    </div>
  );
}

// ── ScanFindings ─────────────────────────────────────────────────

type ScanFindingsProps = {
  findings: ScanFinding[];
  scanStatus: string;
};

export function ScanFindings({ findings, scanStatus }: ScanFindingsProps) {
  if (findings.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-12 gap-3">
        <ShieldCheck size={28} className="text-accent" />
        <div className="text-sm text-text-secondary">
          {scanStatus === "clean"
            ? "No security findings detected."
            : "No findings to display."}
        </div>
      </div>
    );
  }

  // Group by severity for summary
  const bySeverity = findings.reduce(
    (acc, f) => {
      acc[f.severity] = (acc[f.severity] || 0) + 1;
      return acc;
    },
    {} as Record<string, number>
  );

  return (
    <div>
      {/* Summary strip */}
      <div className="flex items-center gap-4 mb-4">
        <ShieldAlert size={14} className="text-text-tertiary" />
        <span className="text-xs text-text-secondary">
          {findings.length} finding{findings.length !== 1 ? "s" : ""}
        </span>
        {Object.entries(bySeverity).map(([sev, count]) => {
          const config = getSeverityConfig(sev);
          return (
            <span
              key={sev}
              className={cn("text-[11px] font-mono", config.text)}
            >
              {count} {config.label.toLowerCase()}
            </span>
          );
        })}
      </div>

      {/* Findings list */}
      <div className="border border-border rounded-lg overflow-hidden">
        {findings.map((finding, i) => (
          <FindingRow key={`${finding.rule}-${finding.file}-${finding.line}-${i}`} finding={finding} />
        ))}
      </div>
    </div>
  );
}
