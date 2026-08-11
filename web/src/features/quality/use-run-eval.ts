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
// RunOptions is what the caller may pin about the run. Both are omitted
// rather than defaulted here: the server defaults the tier to "full" and the
// worker defaults the panel, and sending a default from the client would mean
// two places decide the same thing.
type RunOptions = { tier?: string; models?: string[] };

export function useRunEval(skillName: string, version?: number) {
  const qc = useQueryClient();
  const mutation = useMutation({
    mutationFn: async ({ tier, models }: RunOptions = {}) => {
      // A held version never advances latest_version, so omitting version
      // here would either 404 (first publish held, latest_version === 0)
      // or evaluate the wrong (previous released) version. When the caller
      // knows the specific version to evaluate — e.g. a held version in the
      // review queue — pass it explicitly. Other call sites intentionally
      // omit it to let the server default to the current latest.
      // Models go without agents: there is one agent adapter, and
      // runner.ParsePanel fills it in. Sending models alone used to fall back
      // to the shipped panel with no error anywhere.
      const res = await rerunEval({
        path: { name: skillName },
        body: {
          ...(version ? { version } : {}),
          ...(tier ? { tier } : {}),
          ...(models?.length ? { models } : {}),
        },
      });
      if (res.error) throw new Error(res.error.detail ?? "Failed to queue eval");
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["skill-evals", skillName] });
    },
  });
  return {
    run: (opts?: RunOptions) => mutation.mutate(opts ?? {}),
    isPending: mutation.isPending,
    isError: mutation.isError,
    error: (mutation.error as Error) ?? null,
  };
}
