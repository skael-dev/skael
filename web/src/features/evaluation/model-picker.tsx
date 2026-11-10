import { useMemo, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { ChevronsUpDown } from "lucide-react";
import { listProviderModels } from "@/api/sdk.gen";
import type { Model, Provider } from "@/api/types.gen";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { throwOnApiError } from "@/lib/api-error";
import { cn } from "@/lib/utils";

// SHOWN caps the rows rendered at once. OpenRouter alone lists hundreds.
const SHOWN = 150;

export const providerModelsKey = (id: string) => ["provider-models", id];

function price(m: Model) {
  if (!m.cost) return "price unknown";
  const f = (n: number) => (n === 0 ? "free" : `$${n < 1 ? n.toFixed(2) : n.toFixed(n % 1 ? 2 : 0)}`);
  return `${f(m.cost.input)} in · ${f(m.cost.output)} out per 1M`;
}

function context(m: Model) {
  if (!m.context) return "";
  return m.context >= 1_000_000 ? `${(m.context / 1_000_000).toFixed(m.context % 1_000_000 ? 1 : 0)}M ctx` : `${Math.round(m.context / 1000)}k ctx`;
}

/**
 * ModelPicker chooses a model on one of `providers`. It lists what each
 * provider offers, newest first, and accepts any typed id: a new model, a
 * private one, or an Azure deployment name.
 */
export function ModelPicker({
  providers,
  value,
  providerId,
  onPick,
  label,
  hint,
}: {
  providers: Provider[];
  value: string;
  providerId?: string;
  onPick: (model: string, providerId: string) => void;
  label: string;
  hint?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const results = useQueries({
    queries: providers.map((p) => ({
      queryKey: providerModelsKey(p.id),
      enabled: open,
      staleTime: 60 * 60 * 1000,
      queryFn: async () => {
        const res = await listProviderModels({ path: { id: p.id } });
        throwOnApiError(`list ${p.name}'s models`, res);
        return res.data!;
      },
    })),
  });

  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const groups = useMemo(() => {
    let left = SHOWN;
    return providers.map((p, i) => {
      const data = results[i]?.data;
      const all = data?.models ?? [];
      const matched = all.filter((m) => words.every((w) => `${m.id} ${m.name} ${p.name}`.toLowerCase().includes(w)));
      const shown = matched.slice(0, Math.max(left, 0));
      left -= shown.length;
      return {
        provider: p,
        models: shown,
        hidden: matched.length - shown.length,
        loading: results[i]?.isLoading,
        problem: data?.problem ?? results[i]?.error?.message,
      };
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [providers, results.map((r) => r.dataUpdatedAt).join(","), query]);

  const typed = query.trim();
  const exact = groups.some((g) => g.models.some((m) => m.id === typed));
  const typedProvider = providerId ?? providers[0]?.id ?? "";

  const pick = (model: string, pid: string) => {
    onPick(model, pid);
    setOpen(false);
    setQuery("");
  };

  return (
    <>
      <button
        type="button"
        aria-label={label}
        disabled={providers.length === 0}
        onClick={() => setOpen(true)}
        className="w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-left flex items-center gap-2 hover:border-border-active transition-colors disabled:opacity-50"
      >
        <span className={cn("font-mono truncate flex-1", !value && "text-text-tertiary font-sans")}>{value || "Choose a model"}</span>
        <ChevronsUpDown size={14} className="text-text-tertiary shrink-0" />
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="overflow-hidden p-0 sm:max-w-[640px]">
          <DialogHeader className="sr-only">
            <DialogTitle>{label}</DialogTitle>
            <DialogDescription>Search the provider's models or type any id.</DialogDescription>
          </DialogHeader>
          <Command
            shouldFilter={false}
            className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-wider [&_[cmdk-group-heading]]:text-text-tertiary [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-2"
          >
            <CommandInput placeholder="Search models, or type an id" value={query} onValueChange={setQuery} />
            <CommandList className="max-h-[420px]">
              {typed && !exact && (
                <CommandGroup heading="Typed id">
                  <CommandItem value={`typed:${typed}`} onSelect={() => pick(typed, typedProvider)}>
                    <span className="font-mono text-sm">Use “{typed}”</span>
                    {hint && <span className="ml-auto text-[11px] text-text-tertiary">{hint}</span>}
                  </CommandItem>
                </CommandGroup>
              )}
              {groups.map((g) => (
                <CommandGroup
                  key={g.provider.id}
                  heading={
                    <span className="flex items-center gap-2">
                      {g.provider.name}
                      {!g.provider.has_key && <span className="text-warning normal-case">needs a key</span>}
                      {g.loading && <span className="normal-case text-text-tertiary">loading…</span>}
                      {g.problem && <span className="normal-case text-text-tertiary">{g.problem}</span>}
                    </span>
                  }
                >
                  {g.models.map((m) => (
                    <CommandItem key={`${g.provider.id}:${m.id}`} value={`${g.provider.id}:${m.id}`} onSelect={() => pick(m.id, g.provider.id)}>
                      <div className="flex flex-col min-w-0 flex-1">
                        <span className="text-sm text-text-primary truncate">{m.name || m.id}</span>
                        <span className="font-mono text-[11px] text-text-tertiary truncate">{m.id}</span>
                      </div>
                      <span className="text-[11px] text-text-tertiary text-right shrink-0 tabular-nums">
                        {[context(m), m.cost || m.name ? price(m) : ""].filter(Boolean).join(" · ")}
                      </span>
                    </CommandItem>
                  ))}
                  {g.hidden > 0 && <div className="px-2 py-1.5 text-[11px] text-text-tertiary">{g.hidden} more; refine the search</div>}
                </CommandGroup>
              ))}
              <CommandEmpty>No model matches. Type the full id and choose “Use”.</CommandEmpty>
            </CommandList>
            <p className="text-[11px] text-text-tertiary m-0 px-3 py-2 border-t border-border">
              Lists come from models.dev, filtered to models that can call tools, or from the endpoint itself. Prices are per million tokens.
            </p>
          </Command>
        </DialogContent>
      </Dialog>
    </>
  );
}
