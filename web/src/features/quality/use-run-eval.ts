import { useMutation, useQueryClient } from "@tanstack/react-query";
import { rerunEval } from "@/api/sdk.gen";

// Enqueueing is owner/admin only server-side (internal/evalqueue/routes.go
// gates rerun-eval on u.IsPrivileged()) — this hook does not enforce that;
// callers must gate the button on the user's role themselves. An eval costs
// real compute and takes 45-90 minutes, so the caller renders a disabled
// button while the mutation is in flight rather than letting a double click
// queue two.
//
// A skill with no registered suite gets one derived first — an extra LLM pass
// and a Docker oracle gate before the panel starts — so the first run on an
// imported skill is longer still.
export function useRunEval(skillName: string, version?: number) {
  const qc = useQueryClient();
  const mutation = useMutation({
    mutationFn: async () => {
      // A held version never advances latest_version, so omitting version
      // here would either 404 (first publish held, latest_version === 0)
      // or evaluate the wrong (previous released) version. When the caller
      // knows the specific version to evaluate — e.g. a held version in the
      // review queue — pass it explicitly. Other call sites intentionally
      // omit it to let the server default to the current latest.
      const res = await rerunEval({
        path: { name: skillName },
        body: version ? { version } : {},
      });
      if (res.error) throw new Error(res.error.detail ?? "Failed to queue eval");
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["skill-evals", skillName] });
    },
  });
  return {
    run: () => mutation.mutate(),
    isPending: mutation.isPending,
    isError: mutation.isError,
    error: (mutation.error as Error) ?? null,
  };
}
