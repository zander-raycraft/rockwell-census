import type { CensusState, OpenIssue } from "./api";

/**
 * CensusViewProps supplies the committed census without client-side reconciliation.
 * @brief: CensusViewProps -> saved company records
 * @returns: props [CensusViewProps] -> census display inputs
 */
interface CensusViewProps {
  state: CensusState | undefined;
}

/**
 * cellText preserves unknown values and formats stored scalar facts.
 * @brief: cellText -> turns a saved field into readable table text
 * @params: value [unknown] -> saved field
 * @returns: text [string] -> original value or an explicit unknown label
 */
function cellText(value: unknown): string {
  if (value == null || value === "") return "Unknown";
  if (value === true) return "Active";
  if (value === false) return "Inactive";
  return typeof value === "string" || typeof value === "number"
    ? String(value).replaceAll("_", " ")
    : "Unknown";
}

/**
 * reviewText labels only open issues attached to this saved record.
 * @brief: reviewText -> summarizes unresolved record issues for screen and PDF
 * @params: issues [OpenIssue[]] -> saved company issues
 * @params: id [string] -> saved record identity
 * @returns: text [string] -> issue labels or no open issues
 */
function reviewText(issues: OpenIssue[], id: string): string {
  const labels = issues
    .filter(
      (issue) =>
        issue.status === "OPEN" && (issue.subject || issue.employee_id) === id,
    )
    .map((issue) => {
      if (issue.kind === "MISSING_FIELD" && issue.field) {
        const field =
          issue.field === "ssn"
            ? "SSN"
            : issue.field === "dob"
              ? "date of birth"
              : issue.field.replaceAll("_", " ");
        return `Missing ${field} — open issue`;
      }
      return `${issue.detail || issue.kind.replaceAll("_", " ")} — open issue`;
    });
  return labels.join("; ") || "No open issues";
}

/**
 * CensusTableProps supplies named columns and saved records.
 * @brief: CensusTableProps -> one readable census table
 * @returns: props [CensusTableProps] -> title, columns and rows
 */
interface CensusTableProps {
  title: string;
  columns: [string, string][];
  rows: Record<string, unknown>[];
}

/**
 * CensusTable renders saved facts with stable record identities.
 * @brief: CensusTable -> displays a census collection as a table
 * @params: props [CensusTableProps] -> title, columns and rows
 * @returns: view [React.JSX.Element] -> labeled facts and unknown values
 */
function CensusTable({ title, columns, rows }: CensusTableProps) {
  return (
    <section className="census-section">
      <h2>
        {title}
        <span>{rows.length}</span>
      </h2>
      {rows.length === 0 ? (
        <p className="muted">No {title.toLowerCase()} saved.</p>
      ) : (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                {columns.map(([field, label]) => (
                  <th key={field} scope="col">
                    {label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={String(row.id)}>
                  {columns.map(([field]) => (
                    <td
                      key={field}
                      className={
                        row[field] == null || row[field] === ""
                          ? "unknown"
                          : undefined
                      }
                    >
                      {cellText(row[field])}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

/**
 * CensusView presents employees, household links and coverage as saved facts.
 * @brief: CensusView -> replaces raw census JSON with readable tables
 * @params: props [CensusViewProps] -> current committed census
 * @returns: view [React.JSX.Element] -> employee, dependent and coverage tables
 */
export function CensusView({ state }: CensusViewProps) {
  if (!state) return <p className="empty">No company census loaded.</p>;
  const people = [...state.employees, ...state.dependents];
  const names = new Map(
    people.map((person) => [
      person.id,
      `${person.first_name} ${person.last_name}`.trim(),
    ]),
  );
  const employees = state.employees.map((person) => ({
    ...person,
    name: names.get(person.id),
    review: reviewText(state.open_issues, person.id),
  }));
  const dependents = state.dependents.map((person) => ({
    ...person,
    name: names.get(person.id),
    review: reviewText(state.open_issues, person.id),
    employee:
      typeof person.employee_id === "string"
        ? names.get(person.employee_id)
        : undefined,
  }));
  const coverage = state.enrollments.map((enrollment) => ({
    ...enrollment,
    review: reviewText(state.open_issues, String(enrollment.id)),
    member: names.get(
      String(enrollment.dependent_id ?? enrollment.employee_id),
    ),
  }));
  return (
    <div className="census-view">
      <div className="print-only">
        <h1>{state.company_name}</h1>
        <p>Saved census</p>
      </div>
      <div className="census-actions">
        <span className="muted">Saved company census</span>
        <button className="browse-button" onClick={() => window.print()}>
          Export PDF
        </button>
      </div>
      <p className="muted">
        Current saved records. Unknown means no confirmed value.{" "}
        {state.open_issues.filter((issue) => issue.status === "OPEN").length}{" "}
        open issues; records with open issues require review before use.
      </p>
      <CensusTable
        title="Employees"
        columns={[
          ["name", "Employee"],
          ["dob", "Date of birth"],
          ["ssn", "SSN"],
          ["employment_status", "Employment"],
          ["hire_date", "Hire date"],
          ["review", "Open issues"],
        ]}
        rows={employees}
      />
      <CensusTable
        title="Dependents"
        columns={[
          ["name", "Member"],
          ["employee", "Employee"],
          ["relationship", "Relationship"],
          ["dob", "Date of birth"],
          ["ssn", "SSN"],
          ["review", "Open issues"],
        ]}
        rows={dependents}
      />
      <CensusTable
        title="Coverage"
        columns={[
          ["member", "Member"],
          ["enrollment_type", "Benefit"],
          ["plan", "Plan"],
          ["coverage_tier", "Coverage"],
          ["status", "Status"],
          ["effective_start", "Starts"],
          ["effective_end", "Ends"],
          ["review", "Open issues"],
        ]}
        rows={coverage}
      />
    </div>
  );
}
