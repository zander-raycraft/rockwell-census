import { afterEach, expect, it, vi } from "vitest";
import { loadState } from "./api";

afterEach(() => vi.unstubAllGlobals());

it.each([
  ["<!doctype html><html>Frontend</html>", "HTML instead of JSON"],
  ["", "empty response"],
])("explains an incorrectly routed API response: %s", async (body, message) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(body)),
  );
  await expect(
    loadState("Example", new AbortController().signal),
  ).rejects.toThrow(message);
});
