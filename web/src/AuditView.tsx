import { useState } from "react";
import { isRecord } from "./api";
import type { AuditEvent, CensusState, OpenIssue } from "./api";

const STAGES = [
  {
    id: "READ",
    title: "Load company records",
    description: "The saved census and existing issues before this document.",
  },
  {
    id: "PARSE",
    title: "Read the document",
    description: "The rows, headers and text found in the uploaded file.",
  },
  {
    id: "EXTRACT",
    title: "Interpret the fields",
    description:
      "The model identifies what each column means and flags uncertainty.",
  },
  {
    id: "FORMAT",
    title: "Build member records",
    description: "The extraction plan is applied to every document row.",
  },
  {
    id: "MAP",
    title: "Reconcile with the census",
    description:
      "People are matched, new facts are applied and conflicts are retained.",
  },
  {
    id: "WRITE",
    title: "Save the result",
    description: "The updated census and issues are committed together.",
  },
];

/**
 * PayloadProps describes one unmodified trace value.
 * @brief: PayloadProps -> raw payload and its recorded type
 * @returns: props [PayloadProps] -> data displayed verbatim or as JSON
 */
interface PayloadProps {
  label: string;
  value: unknown;
  type?: string;
}

/**
 * Payload renders exact content without interpreting census facts.
 * @brief: Payload -> shows a recorded input or output
 * @params: props [PayloadProps] -> label, payload and recorded type
 * @returns: view [React.JSX.Element] -> readable raw payload
 */
export function Payload({ label, value, type }: PayloadProps) {
  const bodyText =
    isRecord(value) && typeof value.body_text === "string"
      ? value.body_text
      : undefined;
  return (
    <div className="payload">
      <div className="payload-label">
        {label}
        {type && <code>{type}</code>}
      </div>
      <pre>{displayPayload(bodyText ?? value)}</pre>
      {bodyText !== undefined && (
        <details className="raw-event">
          <summary>Full request / response envelope</summary>
          <pre>{JSON.stringify(value, null, 2)}</pre>
        </details>
      )}
    </div>
  );
}

/**
 * displayPayload formats serialized JSON for inspection while preserving plain text.
 * @brief: displayPayload -> formats trace content without changing its values
 * @params: value [unknown] -> original payload or recorded body text
 * @returns: text [string] -> readable payload
 */
function displayPayload(value: unknown): string {
  if (typeof value !== "string") return JSON.stringify(value ?? null, null, 2);
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

/**
 * AuditViewProps describes the selected run's actual trace.
 * @brief: AuditViewProps -> events and observed run status
 * @returns: props [AuditViewProps] -> trace display inputs
 */
interface AuditViewProps {
  events: AuditEvent[];
  status: string;
  error: string;
}

/**
 * AuditView groups original calls under the pipeline step that produced them.
 * @brief: AuditView -> presents one job as six readable steps
 * @params: props [AuditViewProps] -> actual events, run status and retrieval error
 * @returns: view [React.JSX.Element] -> stage results with expandable call evidence
 */
export function AuditView({ events, status, error }: AuditViewProps) {
  const [selectedStep, setSelectedStep] = useState("all");
  const groups = new Map<string, AuditEvent[]>();
  let current = "REQUEST";
  for (const event of events) {
    if (
      event.component === "pipeline" &&
      STAGES.some((stage) => stage.id === event.operation)
    )
      current = event.operation;
    if (event.component === "pipeline" && event.operation === "LATENCY")
      current = "RESPONSE";
    const group = groups.get(current) ?? [];
    group.push(event);
    groups.set(current, group);
  }
  const committed = events.find(
    (event) =>
      event.component === "history" && event.operation === "committed_changes",
  );
  const changes =
    committed &&
    isRecord(committed.output) &&
    Array.isArray(committed.output.fact_changes)
      ? committed.output.fact_changes.filter(isRecord)
      : [];
  const total = events.find(
    (event) =>
      event.component === "pipeline" &&
      event.operation === "LATENCY" &&
      event.status === "finished",
  );
  const navigation = [
    { id: "REQUEST", title: "Document received" },
    ...STAGES,
    ...(groups.has("RESPONSE") ? [{ id: "RESPONSE", title: "Response" }] : []),
  ];
  return (
    <>
      <p className="job-overview">
        {status === "completed"
          ? "Document processed and saved."
          : status === "failed"
            ? "This job reported a failure. Review the last recorded step."
            : status === "running"
              ? "Processing document. Steps update as the server records them."
              : "Showing the available processing record."}
        {total && (
          <span> Total {(total.elapsed_ms / 1000).toFixed(1)} seconds.</span>
        )}
      </p>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <nav className="step-navigation" aria-label="Processing steps">
        <button
          type="button"
          aria-pressed={selectedStep === "all"}
          onClick={() => setSelectedStep("all")}
        >
          All steps
        </button>
        {navigation.map((step) => {
          const records = groups.get(step.id) ?? [];
          const timing = records.find((event) =>
            step.id === "REQUEST"
              ? event.component === "http" && event.operation === "request"
              : step.id === "RESPONSE"
                ? event.component === "http" &&
                  event.operation === "transport_write"
                : event.component === "pipeline" &&
                  event.operation === step.id &&
                  event.status === "finished",
          );
          return (
            <button
              key={step.id}
              type="button"
              aria-pressed={selectedStep === step.id}
              disabled={records.length === 0}
              onClick={() => setSelectedStep(step.id)}
            >
              <span>{step.title}</span>
              <small>
                {timing
                  ? `${timing.elapsed_ms.toFixed(0)} ms`
                  : records.length
                    ? status === "running"
                      ? "In progress"
                      : "Timing unavailable"
                    : "Not reached"}
              </small>
            </button>
          );
        })}
      </nav>
      {selectedStep === "REQUEST" && (
        <p className="muted">
          Server request receipt and decoding time; browser upload time is not
          recorded here.
        </p>
      )}
      {groups.has("REQUEST") &&
        (selectedStep === "all" || selectedStep === "REQUEST") && (
          <CallList
            title="Document received · request details"
            events={groups.get("REQUEST") ?? []}
            expanded={selectedStep === "REQUEST"}
          />
        )}
      <ol className="job-steps">
        {STAGES.map((stage, index) => {
          if (selectedStep !== "all" && selectedStep !== stage.id) return null;
          const records = groups.get(stage.id) ?? [];
          const result = records.find(
            (event) =>
              event.component === "pipeline" &&
              event.operation === stage.id &&
              event.status === "finished",
          );
          const input = records.find(
            (event) =>
              event.component === "pipeline" &&
              event.operation === stage.id &&
              event.status === "input",
          );
          const failures = records.filter(
            (event) => event.status === "error" || event.status === "failed",
          );
          const outcome = result
            ? "Complete"
            : failures.length
              ? "Failed"
              : records.length
                ? status === "running"
                  ? "Running"
                  : "Incomplete"
                : "Not reached";
          return (
            <li className="job-step" key={stage.id}>
              <div className="step-number">{index + 1}</div>
              <div className="step-content">
                <div className="step-heading">
                  <h2>{stage.title}</h2>
                  <span
                    className={`badge ${result ? "completed" : failures.length ? "failed" : ""}`}
                  >
                    {outcome}
                    {result ? ` · ${result.elapsed_ms.toFixed(0)} ms` : ""}
                  </span>
                </div>
                <p className="muted">{stage.description}</p>
                {failures.map((event) => (
                  <p className="error" key={event.sequence}>
                    {event.error ||
                      `${event.component}: ${event.operation} failed`}
                  </p>
                ))}
                {result && (
                  <StageResult
                    stage={stage.id}
                    value={result.output}
                    changes={stage.id === "WRITE" ? changes : []}
                  />
                )}
                {input && (
                  <details className="raw-event">
                    <summary>Exact data entering this step</summary>
                    <Payload
                      label="Input"
                      value={input.input}
                      type={input.input_type}
                    />
                  </details>
                )}
                {result && (
                  <details className="raw-event">
                    <summary>Full step output</summary>
                    <Payload
                      label="Output"
                      value={result.output}
                      type={result.output_type}
                    />
                  </details>
                )}
                {records.length > 0 && (
                  <CallList
                    title={`${records.length} recorded calls and events`}
                    events={records}
                    expanded={selectedStep === stage.id}
                  />
                )}
              </div>
            </li>
          );
        })}
      </ol>
      {groups.has("RESPONSE") &&
        (selectedStep === "all" || selectedStep === "RESPONSE") && (
          <CallList
            title="Response and total timing"
            events={groups.get("RESPONSE") ?? []}
            expanded={selectedStep === "RESPONSE"}
          />
        )}
    </>
  );
}

/**
 * StageResultProps supplies one observed stage result and committed changes.
 * @brief: StageResultProps -> stage-specific presentation data
 * @returns: props [StageResultProps] -> recorded output
 */
interface StageResultProps {
  stage: string;
  value: unknown;
  changes: Record<string, unknown>[];
}

/**
 * StageResult presents useful facts from a recorded result before the raw evidence.
 * @brief: StageResult -> formats mappings, rows, decisions and saved changes
 * @params: props [StageResultProps] -> original stage output and committed changes
 * @returns: view [React.JSX.Element | null] -> readable stage result
 */
function StageResult({ stage, value, changes }: StageResultProps) {
  if (stage === "MAP" && typeof value === "string")
    return (
      <ul className="decisions">
        {value
          .split("\n")
          .filter((line) => line !== "" && !line.startsWith("summary:"))
          .map((line, index) => (
            <li key={index}>{line}</li>
          ))}
      </ul>
    );
  if (!isRecord(value)) return null;
  if (stage === "READ" || stage === "WRITE") {
    const counts = [
      "employees",
      "dependents",
      "enrollments",
      "open_issues",
    ].map((key) => ({
      key,
      count: Array.isArray(value[key]) ? value[key].length : 0,
    }));
    return (
      <>
        <div className="result-counts">
          {counts.map(({ key, count }) => (
            <span key={key}>
              <strong>{count}</strong>{" "}
              {key === "open_issues" ? "issue records" : key}
            </span>
          ))}
        </div>
        {changes.length > 0 && (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Record</th>
                  <th>Field</th>
                  <th>Before</th>
                  <th>Saved value</th>
                </tr>
              </thead>
              <tbody>
                {changes.map((change, index) => (
                  <tr key={index}>
                    <td>
                      {String(change.table).replaceAll("_", " ")}
                      <small className="record-id">
                        {String(change.row_id)}
                      </small>
                    </td>
                    <td>{String(change.field).replaceAll("_", " ")}</td>
                    <td>{displayPayload(change.before ?? "Unknown")}</td>
                    <td>{displayPayload(change.after ?? "Unknown")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </>
    );
  }
  if (stage === "PARSE")
    return (
      <>
        <p className="stage-fact">
          {typeof value.row_count === "number"
            ? `${value.row_count} data rows`
            : "Document text"}{" "}
          · {String(value.kind ?? "Unknown format")}
        </p>
        {Array.isArray(value.headers) && (
          <div className="header-chips">
            {value.headers.map((header, index) => (
              <span key={index}>{String(header)}</span>
            ))}
          </div>
        )}
      </>
    );
  if (stage === "EXTRACT") {
    const mappings = Array.isArray(value.mappings)
      ? value.mappings.filter(isRecord)
      : [];
    return (
      <>
        <p className="stage-fact">
          Intent: {String(value.intent ?? "Unknown").replaceAll("_", " ")}
        </p>
        {mappings.length > 0 && (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Document column</th>
                  <th>Used as</th>
                </tr>
              </thead>
              <tbody>
                {mappings.map((mapping, index) => (
                  <tr key={index}>
                    <td>{String(mapping.source ?? "Unknown")}</td>
                    <td>
                      {String(mapping.destination ?? "Unknown")}
                      {mapping.literal ? ` · ${String(mapping.literal)}` : ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {Array.isArray(value.uncertainties) &&
          value.uncertainties.map((note, index) => (
            <p className="stage-fact" key={index}>
              {String(note)}
            </p>
          ))}
      </>
    );
  }
  if (stage === "FORMAT") {
    const people = Array.isArray(value.people)
      ? value.people.filter(isRecord)
      : [];
    return (
      <>
        <p className="stage-fact">{people.length} proposed member records</p>
        {people.length > 0 && (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Member</th>
                  <th>Role</th>
                  <th>Date of birth</th>
                  <th>Relationship</th>
                </tr>
              </thead>
              <tbody>
                {people.map((person, index) => (
                  <tr key={index}>
                    <td>
                      {[person.first_name, person.last_name]
                        .filter(Boolean)
                        .join(" ") || "Unknown"}
                    </td>
                    <td>{String(person.role || "Unknown")}</td>
                    <td>{String(person.dob || "Unknown")}</td>
                    <td>{String(person.relationship || "Unknown")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </>
    );
  }
  return null;
}

/**
 * CallListProps supplies original events owned by one process step.
 * @brief: CallListProps -> expandable evidence collection
 * @returns: props [CallListProps] -> title and original events
 */
interface CallListProps {
  title: string;
  events: AuditEvent[];
  expanded?: boolean;
}

/**
 * CallList keeps every original call available beneath the readable results.
 * @brief: CallList -> reveals detailed evidence on demand
 * @params: props [CallListProps] -> title and exact recorded events
 * @returns: view [React.JSX.Element] -> nested audit evidence
 */
function CallList({ title, events, expanded = false }: CallListProps) {
  return (
    <details key={String(expanded)} className="call-list" open={expanded}>
      <summary>{title}</summary>
      {events.map((event) => (
        <details className="audit-event" key={event.sequence}>
          <summary>
            <span className="event-number">{event.sequence}</span>
            <span className="event-title">
              <strong>
                {event.component} / {event.operation}
              </strong>
              <small>{new Date(event.timestamp).toLocaleTimeString()}</small>
            </span>
            <span className="badge">{event.status}</span>
            <span className="event-time">{event.elapsed_ms.toFixed(1)} ms</span>
          </summary>
          <div className="event-body">
            <Payload
              label="Input"
              value={event.input}
              type={event.input_type}
            />
            <Payload
              label="Output"
              value={event.output}
              type={event.output_type}
            />
            <details className="raw-event">
              <summary>Original event and timestamps</summary>
              <Payload label="Event" value={event} />
            </details>
          </div>
        </details>
      ))}
    </details>
  );
}

interface IssueChange {
  runID: string;
  at: string;
  document: string;
  changes: Record<string, unknown>[];
}

/**
 * issueChanges reads committed field transitions from existing history events.
 * @brief: issueChanges -> collects documented changes to one saved issue
 * @params: events [AuditEvent[]] -> latest available company trace
 * @params: issueID [string] -> persistent issue identity
 * @returns: history [IssueChange[]] -> ordered changes grouped by processing document
 */
export function issueChanges(
  events: AuditEvent[],
  issueID: string,
): IssueChange[] {
  const runs = new Map<string, Record<string, unknown>>();
  for (const event of events) {
    if (event.component !== "history" || !isRecord(event.output)) continue;
    const candidates =
      event.operation === "prior_runs" && Array.isArray(event.output.runs)
        ? event.output.runs
        : event.operation === "committed_changes"
          ? [event.output]
          : [];
    for (const run of candidates)
      if (isRecord(run) && typeof run.run_id === "string")
        runs.set(run.run_id, run);
  }
  const history: IssueChange[] = [];
  for (const [runID, run] of runs) {
    const changes = Array.isArray(run.fact_changes)
      ? run.fact_changes
          .filter(isRecord)
          .filter(
            (change) =>
              change.table === "open_issues" && change.row_id === issueID,
          )
      : [];
    if (changes.length)
      history.push({
        runID,
        at: typeof run.at === "string" ? run.at : "",
        document:
          isRecord(run.trigger_document) &&
          typeof run.trigger_document.filename === "string"
            ? run.trigger_document.filename
            : runID,
        changes,
      });
  }
  return history.sort((left, right) => left.at.localeCompare(right.at));
}

/**
 * IssuesViewProps describes saved issues and their available processing history.
 * @brief: IssuesViewProps -> committed state and log-derived evidence
 * @returns: props [IssuesViewProps] -> issue list inputs
 */
interface IssuesViewProps {
  state: CensusState | undefined;
  events: AuditEvent[];
  historyError: string;
  onRun: (id: string) => void;
}

/**
 * issueMember finds a saved person without guessing from ambiguous claims.
 * @brief: issueMember -> labels a bound issue with its saved member name
 * @params: issue [OpenIssue] -> saved issue
 * @params: state [CensusState] -> current saved census
 * @returns: label [string] -> saved member name or issue subject
 */
function issueMember(issue: OpenIssue, state: CensusState): string {
  const people = [...state.employees, ...state.dependents];
  const member =
    people.find((person) => person.id === issue.subject) ??
    people.find((person) => person.id === issue.employee_id);
  return member
    ? `${member.first_name} ${member.last_name}`
    : issue.subject || "Document review";
}

/**
 * IssuesView keeps open and resolved issues visible with their original claims.
 * @brief: IssuesView -> displays persistent issue state and committed change history
 * @params: props [IssuesViewProps] -> census, trace history and run navigation
 * @returns: view [React.JSX.Element] -> auditable member issue list
 */
export function IssuesView({
  state,
  events,
  historyError,
  onRun,
}: IssuesViewProps) {
  const [filter, setFilter] = useState("OPEN");
  if (!state)
    return <p className="empty">Load a company to view its saved issues.</p>;
  const issues = state.open_issues.filter(
    (issue) => filter === "all" || issue.status === filter,
  );
  return (
    <>
      <div className="audit-toolbar">
        <span>
          {state.open_issues.filter((issue) => issue.status === "OPEN").length}{" "}
          open ·{" "}
          {
            state.open_issues.filter((issue) => issue.status === "RESOLVED")
              .length
          }{" "}
          resolved
        </span>
        <label>
          Status{" "}
          <select
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          >
            <option value="OPEN">Open</option>
            <option value="RESOLVED">Resolved</option>
            <option value="all">All issues</option>
          </select>
        </label>
      </div>
      <p className="muted history-note">
        History reflects available saved traces. The processing document is the
        trigger for a change; retained evidence may originate in an earlier
        document.
      </p>
      {historyError && (
        <p role="alert" className="error">
          Issue history unavailable: {historyError}
        </p>
      )}
      {issues.length === 0 && (
        <p className="empty">
          No {filter === "all" ? "" : filter.toLowerCase()} issues.
        </p>
      )}
      {issues.map((issue) => {
        const history = issueChanges(events, issue.id);
        return (
          <details key={issue.id} className="issue-card">
            <summary>
              <span>
                <strong>{issueMember(issue, state)}</strong>
                <small>
                  {issue.kind.replaceAll("_", " ")}
                  {issue.field ? ` · ${issue.field}` : ""}
                </small>
              </span>
              <span
                className={`badge ${issue.status === "RESOLVED" ? "completed" : ""}`}
              >
                {issue.status}
              </span>
            </summary>
            <div className="event-body">
              <p>{issue.detail}</p>
              <code className="muted">{issue.id}</code>
              {(issue.claims ?? []).map((claim) => (
                <Payload key={claim} label="Retained claim" value={claim} />
              ))}
              <h3>Issue history</h3>
              {history.length === 0 && (
                <p className="muted">
                  No recorded transitions available for this issue.
                </p>
              )}
              {history.map((entry) => (
                <div className="issue-history" key={entry.runID}>
                  <button
                    className="text-button"
                    onClick={() => onRun(entry.runID)}
                  >
                    {entry.document} ↗
                  </button>
                  <small>
                    {entry.at
                      ? new Date(entry.at).toLocaleString()
                      : "Unknown time"}
                  </small>
                  {entry.changes
                    .filter((change) => change.field === "status")
                    .map((change) => (
                      <div key={String(change.field)} className="change">
                        <code>{String(change.field)}</code>
                        <pre>
                          {change.before == null
                            ? "unknown"
                            : JSON.stringify(change.before)}{" "}
                          →{" "}
                          {change.after == null
                            ? "unknown"
                            : JSON.stringify(change.after)}
                        </pre>
                      </div>
                    ))}
                  <details className="raw-event">
                    <summary>All recorded issue changes</summary>
                    <Payload
                      label="Committed field changes"
                      value={entry.changes}
                    />
                  </details>
                </div>
              ))}
            </div>
          </details>
        );
      })}
    </>
  );
}
