import { QueryClient } from "@tanstack/react-query";
import { client } from "@/api/client.gen";

client.setConfig({
  baseUrl: "",
});

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
});
