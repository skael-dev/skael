import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";
import { writeFileSync } from "fs";

// The server embeds this SPA via `//go:embed dist/*`. A committed
// web/dist/.gitkeep keeps that glob satisfied on fresh checkouts so the Go
// server build (which must run before the SPA build, to generate openapi.json)
// doesn't fail. Vite's emptyOutDir wipes the placeholder at build start, so
// recreate it after the bundle is written — otherwise a local `npm run build`
// deletes it from disk and the deletion gets accidentally committed (which is
// exactly what broke CI in commit 312f5f0).
function keepDistGitkeep() {
  return {
    name: "keep-dist-gitkeep",
    closeBundle() {
      writeFileSync(path.resolve(__dirname, "dist/.gitkeep"), "");
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), keepDistGitkeep()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
