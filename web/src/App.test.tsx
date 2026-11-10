import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { App } from "./App";
import { IssuesView } from "./AuditView";
import type { AuditEvent, CensusState } from "./api";

const STATE: CensusState = {
  company_id: "company-1",
  company_name: "Example",
  employees: [{ id: "person-1", first_name: "Nora", last_name: "Quill" }],
  dependents: [],
  enrollments: [],
  open_issues: [],
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/**
 * installAPI supplies fixed server responses without a network or model call.
 * @brief: installAPI -> mocks only the HTTP boundary used by the screen
 * @params: hasFailure [boolean] -> whether submission returns an error
 * @returns: submitted [string[]] -> filenames observed in submission order
 */
function installAPI(hasFailure = false): string[] {
  const submitted: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path === "/api/companies") return new Response("[]");
      if (path.includes("/runs?")) return new Response("[]");
      if (path.includes("/state?")) return new Response(JSON.stringify(STATE));
      if (path.includes("/logs/")) return new Response("");
      if (path === "/api/run" && options?.body instanceof FormData) {
        const file = options.body.get("file");
        if (file instanceof File) submitted.push(file.name);
        return new Response(
          JSON.stringify(
            hasFailure
              ? { run_id: "run-failed", error: "Extraction failed" }
              : { run_id: `run-${submitted.length}`, state: STATE },
          ),
          { status: hasFailure ? 500 : 200 },
        );
      }
      throw new Error(`Unexpected request ${path}`);
    }),
  );
  return submitted;
}

/**
 * openCompany uses the same form as a person opening the workspace.
 * @brief: openCompany -> loads the fixed company
 * @returns: done [Promise<void>] -> saved census visible
 */
async function openCompany(): Promise<void> {
  fireEvent.change(screen.getByLabelText("Company"), {
    target: { value: "Example" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Open company" }));
  await screen.findByText("1 employees · 0 dependents · 0 enrollments");
}

describe("document workspace", () => {
  it("keeps picked and dropped files local, avoids duplicates and submits the batch in order", async () => {
    const submitted = installAPI();
    render(<App />);
    await openCompany();
    const first = new File(["Name\nNora"], "first.csv", {
      type: "text/csv",
      lastModified: 1,
    });
    const second = new File(["Relationship\nChild"], "second.csv", {
      type: "text/csv",
      lastModified: 2,
    });
    fireEvent.change(screen.getByLabelText("Choose documents"), {
      target: { files: [first] },
    });
    fireEvent.drop(screen.getByRole("region", { name: "Document upload" }), {
      dataTransfer: { files: [first, second] },
    });
    expect(screen.getByText("2 selected")).toBeTruthy();
    expect(submitted).toEqual([]);
    fireEvent.click(screen.getByRole("button", { name: "Process documents" }));
    await waitFor(() => expect(submitted).toEqual(["first.csv", "second.csv"]));
    await waitFor(() =>
      expect(
        screen.queryByRole("region", { name: "Selected files" }),
      ).toBeNull(),
    );
  });

  it("preserves the saved census and selected document when extraction fails", async () => {
    installAPI(true);
    render(<App />);
    await openCompany();
    fireEvent.change(screen.getByLabelText("Choose documents"), {
      target: { files: [new File(["x"], "failed.csv")] },
    });
    fireEvent.click(screen.getByRole("button", { name: "Process documents" }));
    await screen.findByText("Extraction failed");
    expect(
      screen.getByText("1 employees · 0 dependents · 0 enrollments"),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Remove failed.csv" }),
    ).toBeTruthy();
  });

  it("shows retained claims and the same issue changing from open to resolved in a later document", () => {
    const onRun = vi.fn();
    const state: CensusState = {
      ...STATE,
      open_issues: [
        {
          id: "issue-1",
          employee_id: "person-1",
          kind: "SSN_CONFLICT",
          detail: "Two SSN claims",
          status: "RESOLVED",
          claims: ["955-14-0001", "955-14-0002"],
        },
      ],
    };
    const events: AuditEvent[] = [
      {
        run_id: "later-run",
        sequence: 1,
        timestamp: "2026-11-10T12:00:00Z",
        component: "history",
        operation: "committed_changes",
        status: "finished",
        elapsed_ms: 1,
        input: null,
        output: {
          run_id: "later-run",
          at: "2026-11-10T12:00:00Z",
          trigger_document: { filename: "correction.txt" },
          fact_changes: [
            {
              table: "open_issues",
              row_id: "issue-1",
              field: "status",
              before: "OPEN",
              after: "RESOLVED",
            },
          ],
        },
      },
    ];
    render(
      <IssuesView
        state={state}
        events={events}
        historyError=""
        onRun={onRun}
      />,
    );
    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "RESOLVED" },
    });
    fireEvent.click(screen.getByText("Nora Quill"));
    expect(screen.getByText("955-14-0001")).toBeTruthy();
    expect(screen.getByText("955-14-0002")).toBeTruthy();
    expect(screen.getByText('"OPEN" → "RESOLVED"')).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "correction.txt ↗" }));
    expect(onRun).toHaveBeenCalledWith("later-run");
  });
});

it("opens a saved company from the selector and switches to another company", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/api/companies")
        return new Response(
          JSON.stringify([
            { id: "a", name: "Alpha" },
            { id: "b", name: "Beta" },
          ]),
        );
      if (path.includes("/runs?")) return new Response("[]");
      if (path.includes("/state?")) {
        const company = new URL(path, "http://localhost").searchParams.get(
          "company",
        );
        return new Response(
          JSON.stringify({
            ...STATE,
            company_id: company === "Alpha" ? "a" : "b",
            company_name: company,
          }),
        );
      }
      throw new Error(`Unexpected request ${path}`);
    }),
  );
  render(<App />);
  await screen.findByRole("option", { name: "Alpha" });
  fireEvent.change(screen.getByLabelText("Switch company"), {
    target: { value: "Alpha" },
  });
  await screen.findByRole("heading", { name: "Alpha" });
  fireEvent.change(screen.getByLabelText("Switch company"), {
    target: { value: "Beta" },
  });
  await screen.findByRole("heading", { name: "Beta" });
  expect(screen.queryByRole("heading", { name: "Alpha" })).toBeNull();
});

/**
 * showFailedRun opens a failed job using only fixed server responses.
 * @brief: showFailedRun -> supplies a selectable failed job and observes actions
 * @returns: calls [Promise<string[]>] -> HTTP mutations requested by the controls
 */
async function showFailedRun(): Promise<string[]> {
  const calls: string[] = [];
  let isRemoved = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path === "/api/companies") return new Response("[]");
      if (path.includes("/runs?"))
        return new Response(
          JSON.stringify(
            isRemoved
              ? []
              : [
                  {
                    id: "failed-run",
                    company: "Example",
                    filename: "failed.csv",
                    status: "failed",
                    started_at: "2026-11-10T12:00:00Z",
                  },
                ],
          ),
        );
      if (path.includes("/state?")) return new Response(JSON.stringify(STATE));
      if (path.includes("/logs/"))
        return new Response(
          JSON.stringify({
            run_id: "failed-run",
            sequence: 1,
            timestamp: "2026-11-10T12:00:00Z",
            component: "http",
            operation: "request",
            status: "ok",
            elapsed_ms: 1,
            input: {},
            output: null,
          }) + "\n",
        );
      if (path === "/api/runs/failed-run" && options?.method === "DELETE") {
        calls.push("remove");
        isRemoved = true;
        return new Response('{"removed":"failed-run"}');
      }
      if (path === "/api/run" && options?.body instanceof FormData) {
        expect(options.body.get("source_system")).toBe("payroll");
        const file = options.body.get("file");
        expect(file instanceof File && file.name).toBe("failed.csv");
        calls.push("retry");
        return new Response(
          JSON.stringify({ run_id: "new-attempt", state: STATE }),
        );
      }
      throw new Error(`Unexpected request ${path}`);
    }),
  );
  render(<App />);
  await openCompany();
  await screen.findByRole("button", { name: "Remove run from history" });
  return calls;
}

it("removes a failed job from company history without clearing the census", async () => {
  const calls = await showFailedRun();
  fireEvent.click(
    screen.getByRole("button", { name: "Remove run from history" }),
  );
  await waitFor(() => expect(calls).toEqual(["remove"]));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /failed.csv/ })).toBeNull(),
  );
  expect(
    screen.getByText("1 employees · 0 dependents · 0 enrollments"),
  ).toBeTruthy();
});

it("retries a failed job using its recovered original file and source namespace", async () => {
  const api = await import("./api");
  vi.spyOn(api, "recoverUpload").mockResolvedValue({
    file: new File(["Original content"], "failed.csv", { type: "text/csv" }),
    sourceSystem: "payroll",
  });
  const calls = await showFailedRun();
  const retry = await screen.findByRole("button", { name: "Retry run" });
  await waitFor(() =>
    expect((retry as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(retry);
  await waitFor(() => expect(calls).toEqual(["retry"]));
  expect(
    screen.getByText("1 employees · 0 dependents · 0 enrollments"),
  ).toBeTruthy();
});
