import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { App } from "./App";
import { CensusView } from "./CensusView";
import type { CensusState } from "./api";

const STATE: CensusState = {
  company_id: "company-1",
  company_name: "Example",
  employees: [
    {
      id: "employee-1",
      first_name: "Nora",
      last_name: "Quill",
      dob: null,
      ssn: "955-14-0001",
      employment_status: true,
    },
  ],
  dependents: [
    {
      id: "dependent-1",
      employee_id: "employee-1",
      first_name: "Ari",
      last_name: "Quill",
      relationship: null,
    },
  ],
  enrollments: [
    {
      id: "enrollment-1",
      employee_id: "employee-1",
      dependent_id: "dependent-1",
      enrollment_type: "MEDICAL",
      status: "ENROLLED",
    },
  ],
  open_issues: [],
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/**
 * readFile inspects the original document at the HTTP boundary.
 * @brief: readFile -> reads an uploaded browser file
 * @params: file [File] -> submitted document
 * @returns: content [Promise<string>] -> original text
 */
function readFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(file);
  });
}

describe("saved census and replies", () => {
  it("shows saved members, household links and unknown facts in readable tables", () => {
    const { container } = render(<CensusView state={STATE} />);
    const [employees, dependents, coverage] = screen.getAllByRole("table");
    expect(
      within(employees).getByRole("cell", { name: "Nora Quill" }),
    ).toBeTruthy();
    expect(
      within(employees).getByRole("cell", { name: "955-14-0001" }),
    ).toBeTruthy();
    expect(
      within(employees).getAllByRole("cell", { name: "Unknown" }).length,
    ).toBeGreaterThan(0);
    expect(
      within(dependents).getByRole("cell", { name: "Ari Quill" }),
    ).toBeTruthy();
    expect(
      within(dependents).getByRole("cell", { name: "Nora Quill" }),
    ).toBeTruthy();
    expect(
      within(dependents).getAllByRole("cell", { name: "Unknown" }).length,
    ).toBeGreaterThan(0);
    expect(
      within(coverage).getByRole("cell", { name: "Ari Quill" }),
    ).toBeTruthy();
    expect(
      within(coverage).getByRole("cell", { name: "MEDICAL" }),
    ).toBeTruthy();
    expect(container.querySelector("pre")).toBeNull();
  });

  it("opens the browser print dialog to export the saved census as PDF", () => {
    const print = vi.spyOn(window, "print").mockImplementation(() => undefined);
    render(<CensusView state={STATE} />);
    fireEvent.click(screen.getByRole("button", { name: "Export PDF" }));
    expect(print).toHaveBeenCalledOnce();
  });

  it.each([false, true])(
    "sends an explicit reply as an original email document and preserves it on failure=%s",
    async (hasFailure) => {
      const submitted: File[] = [];
      vi.stubGlobal(
        "fetch",
        vi.fn(async (path: string, options?: RequestInit) => {
          if (path.includes("/runs?")) return new Response("[]");
          if (path.includes("/state?"))
            return new Response(JSON.stringify(STATE));
          if (path.includes("/logs/")) return new Response("");
          if (path === "/api/run" && options?.body instanceof FormData) {
            expect(options.method).toBe("POST");
            expect(options.body.get("company")).toBe("Example");
            const file = options.body.get("file");
            if (!(file instanceof File))
              throw new Error("Reply was not uploaded as a file.");
            submitted.push(file);
            return new Response(
              JSON.stringify(
                hasFailure
                  ? { run_id: "reply-run", error: "Reply processing failed" }
                  : { run_id: "reply-run", state: STATE },
              ),
              { status: hasFailure ? 500 : 200 },
            );
          }
          throw new Error(`Unexpected request ${path}`);
        }),
      );
      render(<App />);
      fireEvent.change(screen.getByLabelText("Company"), {
        target: { value: "Example" },
      });
      fireEvent.click(screen.getByRole("button", { name: "Open company" }));
      await screen.findByText("1 employees · 1 dependents · 1 enrollments");
      fireEvent.click(screen.getByRole("tab", { name: /Issues/ }));
      const reply =
        "  Nora Quill and N. Quill are the same person.\nTheir SSN is 955-14-0001.  ";
      fireEvent.change(screen.getByLabelText("Reply with a correction"), {
        target: { value: reply },
      });
      expect(submitted).toEqual([]);
      fireEvent.click(screen.getByRole("button", { name: "Submit reply" }));
      await waitFor(() => expect(submitted).toHaveLength(1));
      expect(submitted[0].name).toBe("email.txt");
      expect(submitted[0].type).toBe("text/plain");
      expect(await readFile(submitted[0])).toBe(reply);
      if (hasFailure) await screen.findByText("Reply processing failed");
      fireEvent.click(screen.getByRole("tab", { name: /Issues/ }));
      await waitFor(() =>
        expect(
          (
            screen.getByLabelText(
              "Reply with a correction",
            ) as HTMLTextAreaElement
          ).value,
        ).toBe(hasFailure ? reply : ""),
      );
      expect(
        screen.getByText("1 employees · 1 dependents · 1 enrollments"),
      ).toBeTruthy();
    },
  );
});
