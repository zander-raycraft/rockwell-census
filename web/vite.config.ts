import { defineConfig, loadEnv } from "vite";

/**
 * configure proxies local API calls without exposing backend credentials.
 * @brief: configure -> reads the backend URL from the server environment
 * @params: context [{ mode: string }] -> Vite environment mode
 * @returns: config [object] -> development proxy and test settings
 */
export function configure({ mode }: { mode: string }) {
  const env = loadEnv(mode, process.cwd(), "");
  return {
    server: {
      proxy: {
        "/api": {
          target: env.BACKEND_URL || "http://localhost:8080",
          changeOrigin: true,
          rewrite: rewritePath,
          timeout: 0,
          proxyTimeout: 0,
        },
      },
    },
    test: { environment: "jsdom" },
  };
}

/**
 * rewritePath removes the frontend proxy prefix.
 * @brief: rewritePath -> maps the browser API path to the Go route
 * @params: path [string] -> frontend request path
 * @returns: path [string] -> backend request path
 */
function rewritePath(path: string): string {
  return path.replace(/^\/api/, "");
}

// Vite requires a default export for its configuration entry point.
export default defineConfig(configure);
