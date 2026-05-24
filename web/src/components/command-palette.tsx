import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { FileText, Layers, BarChart2, Settings } from "lucide-react";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import { listSkills } from "@/api/sdk.gen";
import type { ListBody } from "@/api/types.gen";

type CommandPaletteProps = {
  open: boolean;
  onClose: () => void;
};

export function CommandPalette({ open, onClose }: CommandPaletteProps) {
  const navigate = useNavigate();

  // Reuse the same query key as skill list — no duplicate fetch when cached
  const { data: listData } = useQuery({
    queryKey: ["skills", "list"],
    queryFn: async () => {
      const res = await listSkills();
      return res.data as ListBody | undefined;
    },
    enabled: open, // lazy-load on first open
    staleTime: 30_000,
  });

  const skills = listData?.skills ?? [];

  // Close on Escape (Command component handles this natively via cmdk,
  // but we also handle it here for the backdrop)
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [open, onClose]);

  const goTo = (path: string) => {
    navigate(path);
    onClose();
  };

  if (!open) return null;

  return (
    /* Backdrop */
    <div
      className="fixed inset-0 z-50 flex items-start justify-center"
      style={{
        paddingTop: "14vh",
        background: "rgba(0,0,0,0.55)",
        backdropFilter: "blur(2px)",
      }}
      onClick={onClose}
    >
      {/* Panel */}
      <div
        className="w-full max-w-[560px] mx-4 flex flex-col overflow-hidden rounded-xl shadow-2xl"
        style={{
          maxHeight: "60vh",
          background: "var(--color-bg-secondary)",
          border: "1px solid var(--color-border-active)",
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <Command
          className="bg-transparent text-text-primary rounded-none border-0"
          style={{ maxHeight: "60vh" }}
        >
          {/* Search input */}
          <CommandInput
            placeholder="Search skills or run a command..."
            className="text-text-primary placeholder:text-text-tertiary border-b border-border h-12 text-sm"
            autoFocus
          />

          {/* Results */}
          <CommandList className="max-h-[calc(60vh-3rem-36px)] overflow-auto py-1.5">
            <CommandEmpty className="py-8 text-center text-sm text-text-tertiary">
              No results found.
            </CommandEmpty>

            {/* Skills group */}
            {skills.length > 0 && (
              <CommandGroup
                heading="Skills"
                className="[&_[cmdk-group-heading]]:text-[10px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-widest [&_[cmdk-group-heading]]:text-text-tertiary [&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:py-1.5"
              >
                {skills.map((skill) => (
                  <CommandItem
                    key={skill.name}
                    value={`${skill.name} ${skill.description}`}
                    onSelect={() => goTo(`/skills/${skill.name}`)}
                    className="flex items-center gap-2.5 px-3 py-2 mx-1.5 rounded-[5px] cursor-pointer text-text-primary data-[selected=true]:bg-bg-tertiary data-[selected=true]:text-text-primary"
                  >
                    <FileText className="size-3.5 shrink-0 text-text-tertiary" />
                    <span className="font-mono text-[13px] text-text-primary shrink-0">
                      {skill.name}
                    </span>
                    {skill.description && (
                      <span className="text-xs text-text-tertiary truncate flex-1 min-w-0">
                        {skill.description}
                      </span>
                    )}
                    <span className="text-[10px] font-mono text-text-tertiary uppercase tracking-wide shrink-0">
                      skill
                    </span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {skills.length > 0 && <CommandSeparator className="my-1 bg-border" />}

            {/* Actions group */}
            <CommandGroup
              heading="Actions"
              className="[&_[cmdk-group-heading]]:text-[10px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-widest [&_[cmdk-group-heading]]:text-text-tertiary [&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:py-1.5"
            >
              <CommandItem
                value="go to skills explorer"
                onSelect={() => goTo("/")}
                className="flex items-center gap-2.5 px-3 py-2 mx-1.5 rounded-[5px] cursor-pointer text-text-primary data-[selected=true]:bg-bg-tertiary data-[selected=true]:text-text-primary"
              >
                <Layers className="size-3.5 shrink-0 text-text-tertiary" />
                <span className="text-[13px]">Go to Skills</span>
                <span className="text-[10px] font-mono text-text-tertiary uppercase tracking-wide ml-auto">
                  action
                </span>
              </CommandItem>
              <CommandItem
                value="go to analytics"
                onSelect={() => goTo("/analytics")}
                className="flex items-center gap-2.5 px-3 py-2 mx-1.5 rounded-[5px] cursor-pointer text-text-primary data-[selected=true]:bg-bg-tertiary data-[selected=true]:text-text-primary"
              >
                <BarChart2 className="size-3.5 shrink-0 text-text-tertiary" />
                <span className="text-[13px]">Go to Analytics</span>
                <span className="text-[10px] font-mono text-text-tertiary uppercase tracking-wide ml-auto">
                  action
                </span>
              </CommandItem>
              <CommandItem
                value="go to settings"
                onSelect={() => goTo("/settings")}
                className="flex items-center gap-2.5 px-3 py-2 mx-1.5 rounded-[5px] cursor-pointer text-text-primary data-[selected=true]:bg-bg-tertiary data-[selected=true]:text-text-primary"
              >
                <Settings className="size-3.5 shrink-0 text-text-tertiary" />
                <span className="text-[13px]">Go to Settings</span>
                <span className="text-[10px] font-mono text-text-tertiary uppercase tracking-wide ml-auto">
                  action
                </span>
              </CommandItem>
            </CommandGroup>
          </CommandList>

          {/* Footer */}
          <div
            className="flex items-center gap-3.5 px-4 py-2 border-t border-border text-[11px] text-text-tertiary font-mono"
          >
            <span className="whitespace-nowrap">↑↓ navigate</span>
            <span className="whitespace-nowrap">↵ select</span>
            <span className="ml-auto whitespace-nowrap">
              {skills.length} skill{skills.length !== 1 ? "s" : ""}
            </span>
          </div>
        </Command>
      </div>
    </div>
  );
}
