import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/desktop/",
  plugins: [react()],
  server: {
    host: true,
    port: 5180,
  },
});
