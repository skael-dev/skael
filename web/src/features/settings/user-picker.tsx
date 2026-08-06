import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { searchUsers } from "@/api/sdk.gen";
import type { PublicUser } from "@/api/types.gen";

// Matches internal/auth/user_store.go's UserStore.Search floor exactly — the
// server ignores anything shorter and returns an empty result, so firing a
// request below this length would only be a wasted round trip.
export const MIN_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 250;

/**
 * Debounced typeahead against GET /api/users/search. Selecting a result
 * calls onSelect and clears the input; the caller owns the selected-members
 * list (so it can also render "already added" state via excludeIds).
 */
export function UserPicker({
  onSelect,
  excludeIds = [],
  placeholder = "Search by name or email…",
  label = "Search users",
}: {
  onSelect: (user: PublicUser) => void;
  excludeIds?: string[];
  placeholder?: string;
  label?: string;
}) {
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  const [open, setOpen] = useState(false);
  const blurTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    const t = setTimeout(() => setDebounced(query.trim()), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [query]);

  useEffect(() => () => {
    if (blurTimer.current) clearTimeout(blurTimer.current);
  }, []);

  const enabled = debounced.length >= MIN_QUERY_LENGTH;

  const { data, isFetching } = useQuery({
    queryKey: ["users", "search", debounced],
    queryFn: async () => {
      const res = await searchUsers({ query: { q: debounced } });
      if (res.error) throw res.error;
      return res.data?.users ?? [];
    },
    enabled,
  });

  const results = (data ?? []).filter((u) => !excludeIds.includes(u.id));

  const handleSelect = (user: PublicUser) => {
    onSelect(user);
    setQuery("");
    setDebounced("");
    setOpen(false);
  };

  return (
    <div className="relative">
      <div className="relative">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-text-tertiary pointer-events-none" />
        <input
          type="text"
          aria-label={label}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          onBlur={() => {
            // Delay so a click on a result registers before we close.
            blurTimer.current = setTimeout(() => setOpen(false), 150);
          }}
          placeholder={placeholder}
          className="w-full h-8 pl-8 pr-2.5 bg-bg-primary border border-border rounded-[5px] text-xs text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors"
        />
      </div>

      {open && query.length > 0 && (
        <div className="absolute z-20 mt-1 w-full bg-bg-secondary border border-border rounded-md shadow-lg overflow-hidden max-h-48 overflow-y-auto">
          {!enabled ? (
            <div className="px-3 py-2 text-[11px] text-text-tertiary">
              Type at least {MIN_QUERY_LENGTH} characters
            </div>
          ) : isFetching ? (
            <div className="px-3 py-2 text-[11px] text-text-tertiary">Searching…</div>
          ) : results.length === 0 ? (
            <div className="px-3 py-2 text-[11px] text-text-tertiary">No matches</div>
          ) : (
            results.map((u) => (
              <button
                key={u.id}
                type="button"
                // onMouseDown fires before the input's onBlur, so the click
                // registers before the dropdown closes.
                onMouseDown={(e) => {
                  e.preventDefault();
                  handleSelect(u);
                }}
                className="w-full text-left px-3 py-2 text-xs hover:bg-bg-tertiary transition-colors cursor-pointer"
              >
                <div className="text-text-primary font-medium">{u.name}</div>
                <div className="text-text-tertiary text-[11px]">{u.email}</div>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}
