import eslint from "@eslint/js";
import { defineConfig } from "eslint/config";
import eslintConfigPrettier from "eslint-config-prettier/flat";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

export default defineConfig(
  {
    ignores: [
      "**/coverage/**",
      "**/dist/**",
      "**/fixtures/**",
      "**/node_modules/**",
      "**/playwright-report/**",
      "**/test-results/**",
      "src/api/wire.generated.ts",
    ],
  },
  {
    files: ["**/*.{js,cjs,mjs}"],
    extends: [eslint.configs.recommended],
  },
  {
    files: ["**/*.{ts,tsx}"],
    extends: [eslint.configs.recommended, tseslint.configs.recommended],
    rules: {
      "@typescript-eslint/no-unused-vars": "off",
      "no-undef": "off",
      "no-unused-vars": "off",
    },
  },
  reactHooks.configs.flat.recommended,
  {
    rules: {
      // Surface Compiler adoption diagnostics without requiring an unrelated
      // rewrite of existing pages. Core Hooks correctness rules stay enabled.
      "react-hooks/refs": "warn",
      "react-hooks/set-state-in-effect": "warn",
    },
  },
  eslintConfigPrettier,
);
