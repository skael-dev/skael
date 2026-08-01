import { useMutation, useQueryClient } from "@tanstack/react-query";
import { rerunEval } from "@/api/sdk.gen";

// Enqueueing is open to any authenticated member, matching the server (no
// privilege check on this route). An eval costs real compute and takes
// 45-90 minutes, so the caller renders a disabled button while the mutation
// is in flight rather than letting a double click queue two.
export function useRunEval(skillName: string) {
  const qc = useQueryClient();
  const mutation = useMutation({
    mutationFn: async () => {
      // Pass an empty body and let the server default the panel/tier rather
      // than inventing values here.
      const res = await rerunEval({ path: { name: skillName }, body: {} });
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
