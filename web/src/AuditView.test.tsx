import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { AuditView } from "./AuditView";
import type { AuditEvent } from "./api";

const EVENT: AuditEvent = {
  run_id: "run-1",
  sequence: 1,
  timestamp: "2026-11-10T12:00:00Z",
  component: "pipeline",
  operation: "READ",
  status: "finished",
  elapsed_ms: 12,
  input: null,
  output: { employees: [], dependents: [], enrollments: [], open_issues: [] },
};

const EVENTS: AuditEvent[] = [
  EVENT,
  {
    ...EVENT,
    sequence: 2,
    operation: "PARSE",
    output: { kind: "csv", row_count: 1, headers: ["Name"] },
  },
  { ...EVENT, sequence: 3, operation: "EXTRACT", status: "started" },
  {
    ...EVENT,
    sequence: 4,
    component: "model",
    operation: "request",
    status: "ok",
    input: "exact original preview",
    output: "exact original model response",
  },
  {
    ...EVENT,
    sequence: 5,
    operation: "EXTRACT",
    output: {
      intent: "REPORTED_FACTS",
      mappings: [{ source: "Name", destination: "employee.full_name" }],
    },
  },
  {
    ...EVENT,
    sequence: 6,
    operation: "FORMAT",
    output: { people: [] },
  },
];

afterEach(cleanup);

it("opens only the selected step's calls and preserves original evidence when returning to all steps", () => {
  render(<AuditView events={EVENTS} status="completed" error="" />);

  fireEvent.click(screen.getByRole("button", { name: /Interpret the fields/ }));
  expect(
    screen.getByRole("heading", { name: "Interpret the fields" }),
  ).toBeTruthy();
  expect(
    screen.queryByRole("heading", { name: "Load company records" }),
  ).toBeNull();
  expect(
    screen.queryByRole("heading", { name: "Build member records" }),
  ).toBeNull();
  expect(screen.getByText("employee.full_name")).toBeTruthy();
  const modelCall = screen.getByText("model / request");
  const calls = modelCall.closest(".call-list");
  expect(calls?.getAttribute("open")).toBe("");
  fireEvent.click(modelCall);
  expect(modelCall.closest("details")?.getAttribute("open")).toBe("");
  expect(screen.getByText("exact original preview")).toBeTruthy();
  expect(screen.getByText("exact original model response")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: /Load company records/ }));
  expect(screen.queryByText("model / request")).toBeNull();
  expect(screen.queryByText("exact original preview")).toBeNull();
  expect(
    screen.getByRole("heading", { name: "Load company records" }),
  ).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "All steps" }));
  expect(
    screen.getByRole("heading", { name: "Load company records" }),
  ).toBeTruthy();
  expect(
    screen.getByRole("heading", { name: "Interpret the fields" }),
  ).toBeTruthy();
  expect(
    screen.getByRole("heading", { name: "Build member records" }),
  ).toBeTruthy();
  expect(screen.getByText("model / request")).toBeTruthy();
});
