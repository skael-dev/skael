import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getEvalSuiteTriggers, reviewEvalSuite } from "@/api/sdk.gen";

type Query = { query: string; should_trigger: boolean };

// A port of skill-creator's assets/eval_review.html. The page's whole job is
// to make review cheap. The rows arrive filled in. A save with no edit is
// a legitimate outcome. That outcome is what raises the suite to authored.
export function TriggerReview({
  suiteRef,
  skillName,
  onDone,
}: {
  suiteRef: string;
  skillName: string;
  onDone?: () => void;
}) {
  const [items, setItems] = useState<Query[]>([]);
  const [result, setResult] = useState<{ changed: boolean } | null>(null);
  const qc = useQueryClient();

  const query = useQuery({
    queryKey: ["suite-triggers", suiteRef],
    queryFn: async () => {
      const res = await getEvalSuiteTriggers({ path: { ref: suiteRef } });
      if (res.error) throw res.error;
      return res.data;
    },
    // This turns off the refetch React Query issues on window focus. The
    // effect below resyncs `items` from every fetched result. An
    // unrequested refetch while a reviewer holds an unsaved edit open
    // silently discards that edit.
    refetchOnWindowFocus: false,
  });

  useEffect(() => {
    if (query.data?.triggers) setItems(query.data.triggers);
  }, [query.data]);

  const save = useMutation({
    mutationFn: async () => {
      const res = await reviewEvalSuite({ path: { ref: suiteRef }, body: { triggers: items } });
      if (res.error) throw res.error;
      return res.data;
    },
    onSuccess: (data) => {
      setResult({ changed: !!data?.changed });
      // The badge and this button are driven by the skill's quality record,
      // which the review just changed. Without this they stay on screen until
      // a manual reload, and the page reads as if the save did nothing.
      qc.invalidateQueries({ queryKey: ["skill-quality", skillName] });
      onDone?.();
    },
  });

  const positive = items.filter((i) => i.should_trigger);
  const negative = items.filter((i) => !i.should_trigger);
  // The server refuses a blank query and an empty list with a 422, because an
  // edit becomes a new authored suite that has to be runnable. Caught here so
  // the reviewer sees it on the row rather than as a failed save.
  const blocked = items.length === 0 || items.some((i) => i.query.trim() === "");

  function update(index: number, patch: Partial<Query>) {
    setItems((prev) => prev.map((item, i) => (i === index ? { ...item, ...patch } : item)));
  }

  function rows(group: Query[], heading: string) {
    if (group.length === 0) return null;
    return (
      <>
        <tr>
          <td colSpan={3} className="bg-bg-tertiary px-3 py-1.5 text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
            {heading}
          </td>
        </tr>
        {group.map((item) => {
          const index = items.indexOf(item);
          return (
            <tr key={index} className="border-b border-border align-top">
              <td className="px-3 py-2">
                <textarea
                  aria-label={`Query ${index + 1}`}
                  value={item.query}
                  onChange={(e) => update(index, { query: e.target.value })}
                  className="w-full min-h-[60px] rounded border border-border bg-transparent p-2 text-[13px]"
                />
              </td>
              <td className="px-3 py-2">
                <input
                  type="checkbox"
                  aria-label={`Should trigger for query ${index + 1}`}
                  checked={item.should_trigger}
                  onChange={(e) => update(index, { should_trigger: e.target.checked })}
                  className="accent-accent"
                />
              </td>
              <td className="px-3 py-2">
                <button
                  onClick={() => setItems((prev) => prev.filter((_, i) => i !== index))}
                  className="text-xs text-danger hover:underline"
                >
                  Delete
                </button>
              </td>
            </tr>
          );
        })}
      </>
    );
  }

  return (
    <div className="mt-6 max-w-[900px]">
      <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-2">
        Review the eval set for {skillName}
      </div>
      <p className="mb-4 text-[13px] text-text-secondary">
        These queries were generated. Reading them is what makes the score mean something. Saving
        with no edit is a valid answer, and it marks the eval set reviewed.
      </p>

      {query.isLoading ? (
        <div className="text-[13px] text-text-tertiary">Loading…</div>
      ) : query.isError ? (
        <div className="text-[13px] text-danger">Could not load the queries</div>
      ) : (
        <>
          <table className="w-full border border-border rounded-lg bg-bg-secondary text-left">
            <thead>
              <tr className="border-b border-border text-[11px] text-text-tertiary">
                <th className="px-3 py-2 w-[70%]">Query</th>
                <th className="px-3 py-2">Fires</th>
                <th className="px-3 py-2">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rows(positive, "Should trigger")}
              {rows(negative, "Should not trigger")}
            </tbody>
          </table>

          <div className="mt-3 flex items-center gap-3">
            <button
              onClick={() => setItems((prev) => [...prev, { query: "", should_trigger: true }])}
              className="text-xs text-accent hover:underline"
            >
              Add query
            </button>
            <button
              onClick={() => save.mutate()}
              disabled={save.isPending || blocked}
              title={blocked ? "Every row needs a query, and the list cannot be empty." : undefined}
              className="text-xs text-accent hover:underline disabled:opacity-50"
            >
              {save.isPending ? "Saving…" : "Save review"}
            </button>
            <span className="text-[11px] text-text-tertiary">
              {positive.length} should trigger, {negative.length} should not
            </span>
          </div>
        </>
      )}

      {/* Nothing re-runs the release decision when origin flips, so the old
          copy ("the existing score can now release a held version") promised
          something that never happens. The next evaluation is what releases. */}
      {result && !result.changed && (
        <p className="mt-3 text-[13px] text-text-secondary">
          Marked as reviewed. A new evaluation is what releases a held version.
        </p>
      )}
      {result && result.changed && (
        <p className="mt-3 text-[13px] text-text-secondary">
          Saved as a new eval set. The existing score measured the old queries, so re-run the
          evaluation to score against these.
        </p>
      )}
      {save.isError && <p className="mt-3 text-[13px] text-danger">Could not save the review</p>}
    </div>
  );
}
