import { QueryClient } from "@tanstack/react-query";
import { client } from "@/api/client.gen";

/** On 401 from any non-auth API route, the session is gone — go to login.
 *  Exported for tests; the default redirect swaps the whole document so all
 *  in-flight query state is dropped with it. */
export function handleUnauthorized(
  response: Response,
  redirect: (to: string) => void = (to) => window.location.assign(to),
): Response {
  if (
    response.status === 401 &&
    !new URL(response.url, window.location.origin).pathname.startsWith("/api/auth/") &&
    window.location.pathname !== "/login"
  ) {
    redirect("/login");
  }
  return response;
}

client.setConfig({
  baseUrl: "",
});

client.interceptors.response.use((response) => handleUnauthorized(response));

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
});
