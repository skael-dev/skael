type Entry = { value: string; at: number };

const cache = new Map<string, Entry>();

export async function lookup(key: string, fetcher: (k: string) => Promise<string>): Promise<string> {
  const hit = cache.get(key);
  if (hit && Date.now() - hit.at < 60_000) {
    return hit.value;
  }
  const value = await fetcher(key);
  cache.set(key, { value, at: Date.now() });
  return value;
}
