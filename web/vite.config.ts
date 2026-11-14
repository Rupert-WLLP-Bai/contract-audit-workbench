// File metadata (manifest mirror):
// Purpose: Configures Vite development/build and API proxy behavior; the settings are consumed by the web toolchain and stay separate from product runtime configuration.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-12
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { originalReaderAssets } from "./build/original-reader-assets";

const apiProxy = process.env.VITE_DEV_API_PROXY ?? "http://127.0.0.1:8080";

export default defineConfig(({ mode }) => {
  const htmlEntry =
    mode === "admin" ? "admin.html" : mode === "portal" ? "portal.html" : "index.html";
  const base = mode === "admin" ? "/admin/" : mode === "portal" ? "/portal/" : "/";
  return {
    plugins: [
      react(),
      ...(mode === "admin" || mode === "portal" ? [] : [originalReaderAssets(base)]),
      {
        name: "development-html-entry",
        apply: "serve",
        configureServer(server) {
          if (mode !== "admin" && mode !== "portal") return;
          server.middlewares.use((request, _response, next) => {
            const pathname = (request.url ?? "").split("?")[0] ?? "";
            if (
              pathname.startsWith(base) &&
              !pathname.endsWith(".html") &&
              request.headers.accept?.includes("text/html")
            ) {
              request.url = `${base}${htmlEntry}`;
            }
            next();
          });
        },
      },
    ],
    base,
    resolve: {
      dedupe: ["react", "react-dom"],
    },
    // Optimize shared React/UI modules together before lazy routes are opened.
    // A late optimization can otherwise leave a long-lived tab using an older
    // renderer while a newly loaded route imports a newer React module URL.
    optimizeDeps: {
      include: [
        "react",
        "react-dom",
        "react-dom/client",
        "react/jsx-runtime",
        "react/jsx-dev-runtime",
        "antd",
        "antd/locale/zh_CN",
        "@ant-design/cssinjs",
        "@ant-design/icons",
        "@tanstack/react-query",
        "react-router",
      ],
    },
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
