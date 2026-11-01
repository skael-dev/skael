import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { toast } from "sonner";
import { createSet, listSkillVersions, updateSet } from "@/api/sdk.gen";
import type { SetDetail, SetEntry } from "@/api/types.gen";
import { Button } from "@/components/ui/button";
import { fetchAllSkillNames } from "@/features/settings/ownership";
import { throwOnApiError } from "@/lib/api-error";

const SET_NAME_RE = /^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$/;

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors";
const labelClass = "text-[11px] font-medium text-text-secondary uppercase tracking-wider";

function EntryRow({
  entry,
  onChange,
  onRemove,
}: {
  entry: SetEntry;
  onChange: (version: number) => void;
  onRemove: () => void;
}) {
  const { data: versions } = useQuery({
    queryKey: ["skill-versions", entry.name],
    queryFn: async () => {
      const res = await listSkillVersions({ path: { name: entry.name } });
      if (res.error) throw res.error;
      return res.data?.versions ?? [];
    },
  });
  const pinned = entry.version ?? 0;
  const known = (versions ?? []).some((v) => v.version === pinned);

  return (
    <div className="flex items-center gap-2 px-3 py-2 border-b border-border last:border-b-0">
      <span className="flex-1 min-w-0 truncate font-mono text-[13px] text-text-primary">
        {entry.name}
      </span>
      <select
        aria-label={`Version of ${entry.name}`}
        value={pinned}
        onChange={(e) => onChange(Number(e.target.value))}
        className="h-8 w-[104px] shrink-0 rounded-[5px] bg-bg-tertiary border border-border px-1.5 text-xs font-mono text-text-secondary outline-none cursor-pointer focus:border-border-active"
      >
        <option value={0}>latest</option>
        {pinned !== 0 && !known && <option value={pinned}>v{pinned}</option>}
        {(versions ?? []).map((v) => (
          <option key={v.version} value={v.version}>
            v{v.version}
            {v.gate_state === "released" ? "" : ` (${v.gate_state.replace("_", " ")})`}
          </option>
        ))}
      </select>
      <button
        type="button"
        aria-label={`Remove ${entry.name}`}
        onClick={onRemove}
        className="flex items-center justify-center size-8 shrink-0 text-text-tertiary hover:text-destructive cursor-pointer bg-transparent border-none"
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}

// Without `initial` this creates a set. A set's name is fixed once it exists.
export function SetEditor({
  initial,
  onSaved,
  onCancel,
}: {
  initial?: SetDetail;
  onSaved: (name: string) => void;
  onCancel: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [entries, setEntries] = useState<SetEntry[]>(initial?.entries ?? []);
  const [adding, setAdding] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);

  const { data: skillNames } = useQuery({
    queryKey: ["skills", "list", "names"],
    queryFn: fetchAllSkillNames,
  });

  const addEntry = () => {
    const skill = adding.trim();
    if (!skill) return;
    if (entries.some((e) => e.name === skill)) {
      setFieldError(`${skill} is already in this set. Change its version in the list below.`);
      return;
    }
    if (skillNames && !skillNames.includes(skill)) {
      setFieldError(`No skill is named ${skill}. Pick a name from the list.`);
      return;
    }
    setEntries((list) => [...list, { name: skill }]);
    setAdding("");
    setFieldError(null);
  };

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        description: description.trim() || undefined,
        entries: entries.map((e) => (e.version ? e : { name: e.name })),
      };
      if (initial) {
        const res = await updateSet({ path: { name: initial.name }, body });
        throwOnApiError("save the set", res);
        return initial.name;
      }
      const res = await createSet({ body: { ...body, name } });
      throwOnApiError("create the set", res);
      return name;
    },
    onSuccess: (saved) => {
      toast.success(initial ? `Saved set ${saved}` : `Created set ${saved}`);
      queryClient.invalidateQueries({ queryKey: ["sets"] });
      onSaved(saved);
    },
    onError: (err) => toast.error(err.message),
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!initial && !SET_NAME_RE.test(name)) {
      setFieldError(
        "A set name uses lowercase letters, digits, colons, dots and hyphens. Correct the name.",
      );
      return;
    }
    setFieldError(null);
    save.mutate();
  };

  return (
    <form
      onSubmit={submit}
      className="flex flex-col gap-3 bg-bg-secondary border border-border rounded-lg p-3.5"
    >
      {!initial && (
        <div className="flex flex-col gap-1.5">
          <label htmlFor="set-name" className={labelClass}>
            Name
          </label>
          <input
            id="set-name"
            autoFocus
            value={name}
            maxLength={128}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. payments-agent"
            className={`${fieldClass} font-mono`}
          />
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <label htmlFor="set-description" className={labelClass}>
          Description
        </label>
        <input
          id="set-description"
          value={description}
          maxLength={1000}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What this set is for"
          className={fieldClass}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <label htmlFor="set-add-skill" className={labelClass}>
          Skills
        </label>
        <div className="flex gap-2">
          <input
            id="set-add-skill"
            list="set-skill-names"
            value={adding}
            onChange={(e) => setAdding(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addEntry();
              }
            }}
            placeholder="Skill name"
            className={`${fieldClass} font-mono min-w-0`}
          />
          <datalist id="set-skill-names">
            {(skillNames ?? [])
              .filter((n) => !entries.some((e) => e.name === n))
              .map((n) => (
                <option key={n} value={n} />
              ))}
          </datalist>
          <Button
            type="button"
            variant="outline"
            className="border-border text-text-secondary"
            onClick={addEntry}
          >
            <Plus className="size-3" />
            Add
          </Button>
        </div>
        {entries.length > 0 ? (
          <div className="border border-border rounded-[5px] bg-bg-primary mt-1">
            {entries.map((entry) => (
              <EntryRow
                key={entry.name}
                entry={entry}
                onChange={(version) =>
                  setEntries((list) =>
                    list.map((e) => (e.name === entry.name ? { name: e.name, version } : e)),
                  )
                }
                onRemove={() =>
                  setEntries((list) => list.filter((e) => e.name !== entry.name))
                }
              />
            ))}
          </div>
        ) : (
          <p className="text-[11px] text-text-tertiary m-0">
            No skills yet. Add a skill by name. A new entry follows the latest released version.
          </p>
        )}
      </div>

      {fieldError && <p className="text-xs text-danger m-0">{fieldError}</p>}
      {save.isError && <p className="text-xs text-danger m-0">{save.error.message}</p>}

      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="border-border text-text-secondary"
          onClick={onCancel}
        >
          Cancel
        </Button>
        <Button
          type="submit"
          size="sm"
          className="bg-accent text-bg-primary hover:bg-accent/90"
          disabled={save.isPending || (!initial && !name)}
        >
          {save.isPending ? "Saving..." : initial ? "Save" : "Create set"}
        </Button>
      </div>
    </form>
  );
}
