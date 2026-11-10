import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { CensusView } from "./CensusView";
import type { CensusState } from "./api";

afterEach(cleanup);

it("updates the printable census when a saved correction resolves the missing SSN issue", () => {
  const state: CensusState = {
    company_id: "company-1",
    company_name: "Example",
    employees: [
      { id: "employee-1", first_name: "Nora", last_name: "Quill", ssn: null },
    ],
    dependents: [
      {
        id: "dependent-1",
        employee_id: "employee-1",
        first_name: "Alex",
        last_name: "Quill",
      },
    ],
    enrollments: [],
    open_issues: [
      {
        id: "issue-1",
        kind: "MISSING_FIELD",
        detail: "Employee SSN is missing",
        status: "OPEN",
        subject: "employee-1",
        employee_id: "employee-1",
        field: "ssn",
      },
      {
        id: "issue-2",
        kind: "MISSING_FIELD",
        detail: "Relationship is missing",
        status: "OPEN",
        subject: "dependent-1",
        employee_id: "employee-1",
        field: "relationship",
      },
    ],
  };
  const { rerender } = render(<CensusView state={state} />);
  expect(screen.getAllByText("Missing SSN — open issue")).toHaveLength(1);
  expect(screen.getAllByText("Missing relationship — open issue")).toHaveLength(
    1,
  );
  expect(screen.getByText(/2 open issues/)).toBeTruthy();
  rerender(
    <CensusView
      state={{
        ...state,
        employees: [{ ...state.employees[0], ssn: "123-45-6789" }],
        open_issues: state.open_issues.map((issue) =>
          issue.id === "issue-1" ? { ...issue, status: "RESOLVED" } : issue,
        ),
      }}
    />,
  );
  expect(screen.queryByText("Missing SSN — open issue")).toBeNull();
  expect(screen.getByText("123-45-6789")).toBeTruthy();
  expect(screen.getByText(/1 open issues/)).toBeTruthy();
});
