import { defineConfig } from "vitest/config";

// The scheduler tests are pure logic (no DOM), so the default "node"
// environment is enough — no need to pull in a browser-like environment
// just to run them.
export default defineConfig({
  test: {
    environment: "node",
  },
});
