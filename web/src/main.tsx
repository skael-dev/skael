import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { queryClient } from "@/lib/query";
import { App } from "@/app/app";
import "@/styles/globals.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
        <Toaster
          theme="dark"
          position="bottom-right"
          richColors
          style={
            {
              "--success-bg": "var(--color-bg-tertiary)",
              "--success-border": "var(--color-border-active)",
              "--success-text": "var(--color-text-primary)",
            } as React.CSSProperties
          }
        />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>
);
