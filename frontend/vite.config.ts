import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Standard Vite + React setup: no extra plugins beyond what's needed to
// compile JSX/TSX, since this project intentionally avoids a heavy UI
// framework in favor of plain CSS and hand-written components.
export default defineConfig({
  plugins: [react()],
});
