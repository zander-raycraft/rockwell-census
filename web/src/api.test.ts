import { afterEach, expect, it, vi } from "vitest";
import { listCompanies, loadState } from "./api";

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

it("recovers the exact failed upload and its source namespace from the saved trace", async () => {
  const { recoverUpload } = await import("./api");
  const multipart =
    '--retry-boundary\r\nContent-Disposition: form-data; name="company"\r\n\r\nExample\r\n--retry-boundary\r\nContent-Disposition: form-data; name="source_system"\r\n\r\npayroll\r\n--retry-boundary\r\nContent-Disposition: form-data; name="file"; filename="census.csv"\r\nContent-Type: text/csv\r\n\r\nName,Role\nNora,Employee\n\r\n--retry-boundary--\r\n';
  const trace = [
    {
      run_id: "failed",
      sequence: 1,
      timestamp: "2026-11-10T15:00:00Z",
      component: "http",
      operation: "request",
      status: "ok",
      elapsed_ms: 1,
      input: {
        company: "Example",
        body_complete: true,
        body: { bytes: btoa(multipart) },
        headers: {
          "Content-Type": ["multipart/form-data; boundary=retry-boundary"],
        },
      },
      output: null,
    },
  ];
  const recovered = await recoverUpload(trace, "Example");
  expect(recovered.file.name).toBe("census.csv");
  expect(recovered.file.type).toBe("text/csv");
  const content = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(recovered.file);
  });
  expect(content).toBe("Name,Role\nNora,Employee\n");
  expect(recovered.sourceSystem).toBe("payroll");
  await expect(recoverUpload(trace, "Another company")).rejects.toThrow(
    "original upload is unavailable",
  );
});

it("explains company-list rate limiting without claiming company data was deleted", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("rate limited", { status: 429 })),
  );
  await expect(listCompanies(new AbortController().signal)).rejects.toThrow(
    "Too many requests (HTTP 429)",
  );
});
