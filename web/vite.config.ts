import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const apiProxy = process.env.VITE_DEV_API_PROXY ?? "http://127.0.0.1:8080";

export default defineConfig(({ mode }) => {
  const htmlEntry = mode === "admin" ? "admin.html" : mode === "portal" ? "portal.html" : "index.html";
  const base = mode === "admin" ? "/admin/" : mode === "portal" ? "/portal/" : "/";
  return {
    plugins: [react(), {
      name: "development-html-entry",
      apply: "serve",
      configureServer(server) {
        if (mode !== "admin" && mode !== "portal") return;
        server.middlewares.use((request, _response, next) => {
          const pathname = (request.url ?? "").split("?")[0] ?? "";
          if (pathname.startsWith(base) && !pathname.endsWith(".html") && request.headers.accept?.includes("text/html")) {
            request.url = `${base}${htmlEntry}`;
          }
          next();
        });
      },
    }],
    base,
    server: {
      port: 5173,
      proxy: {
        "/openapi.json": {
          target: apiProxy,
          changeOrigin: true,
        },
        "^/api(?:/|$)": {
          target: apiProxy,
          changeOrigin: true,
        },
      },
    },
    build: {
      rollupOptions: {
        input: { index: htmlEntry },
      },
    },
  };
});
