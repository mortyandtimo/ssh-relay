import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiProxyTarget = env.DESKTOP_CONSOLE_API_PROXY_TARGET || env.VITE_API_BASE_URL || "http://127.0.0.1:7710";
  const proxy = {
    "/api": {
      target: apiProxyTarget,
      changeOrigin: true,
    },
  };
  return {
    base: "/node/",
    plugins: [react()],
    server: {
      host: true,
      port: 5181,
      proxy,
    },
    preview: {
      host: true,
      port: 4175,
      proxy,
    },
  };
});
