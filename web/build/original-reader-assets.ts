// File metadata (manifest mirror):
// Purpose: Bundles pinned PDF reader workers, fonts, decoders and Chinese/English OCR resources as same-origin development and production assets without external CDN reads.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import type { Plugin } from "vite";

/** Ship pinned reader workers, fonts and OCR data from dependencies, including offline deployments. */
export function originalReaderAssets(base: string): Plugin {
  const require = createRequire(import.meta.url);
  const pdf = dirname(require.resolve("pdfjs-dist/package.json"));
  const ocr = dirname(require.resolve("tesseract.js/package.json"));
  const core = dirname(require.resolve("tesseract.js-core/package.json", { paths: [ocr] }));
  const files = new Map<string, string>();
  const add = (target: string, path: string) => files.set(`document-reader/${target}`, path);
  const directory = (target: string, path: string, filter: (name: string) => boolean = () => true) => {
    for (const entry of readdirSync(path, { withFileTypes: true }))
      if (entry.isFile() && filter(entry.name)) add(`${target}/${entry.name}`, join(path, entry.name));
  };
  add("pdf/pdf.worker.min.js", join(pdf, "legacy/build/pdf.worker.min.mjs"));
  add("pdf/LICENSE", join(pdf, "LICENSE"));
  directory("pdf/cmaps", join(pdf, "cmaps"));
  directory("pdf/fonts", join(pdf, "standard_fonts"));
  directory("pdf/wasm", join(pdf, "wasm"));
  add("ocr/worker.min.js", join(ocr, "dist/worker.min.js"));
  add("ocr/LICENSE", join(ocr, "LICENSE.md"));
  directory("ocr/core", core, (name) => /^tesseract-core.*\.wasm(?:\.js)?$/.test(name) || name === "LICENSE");
  for (const language of ["chi_sim", "eng"]) {
    const root = dirname(require.resolve(`@tesseract.js-data/${language}/package.json`));
    add(`ocr/lang/${language}.traineddata.gz`, join(root, `4.0.0_best_int/${language}.traineddata.gz`));
  }
  return {
    name: "original-reader-local-assets",
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const path = new URL(request.url ?? "/", "http://localhost").pathname;
        if (!path.startsWith(`${base}document-reader/`)) return next();
        const source = files.get(path.slice(base.length));
        if (!source) { response.statusCode = 404; response.end(); return; }
        response.setHeader("Content-Type", source.endsWith(".wasm") ? "application/wasm"
          : /\.(?:m?js)$/.test(source) ? "application/javascript" : "application/octet-stream");
        response.end(readFileSync(source));
      });
    },
    generateBundle() {
      for (const [fileName, source] of files) this.emitFile({ type: "asset", fileName, source: readFileSync(source) });
    },
  };
}
