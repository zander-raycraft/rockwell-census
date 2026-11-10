/**
 * Member describes saved member identity and original server fields.
 * @brief: Member -> saved member identity and original server fields
 * @returns: value [Member] -> validated server boundary
 */
export interface Member {
  id: string;
  first_name: string;
  last_name: string;
  [field: string]: unknown;
}

/**
 * OpenIssue describes persistent issue status and retained claims.
 * @brief: OpenIssue -> persistent issue status and retained claims
 * @returns: value [OpenIssue] -> validated server boundary
 */
export interface OpenIssue {
  id: string;
  kind: string;
  detail: string;
  status: "OPEN" | "RESOLVED";
  employee_id?: string | null;
  subject?: string | null;
  field?: string | null;
  claims?: string[] | null;
}

/**
 * CensusState describes committed company census collections.
 * @brief: CensusState -> committed company census collections
 * @returns: value [CensusState] -> validated server boundary
 */
export interface CensusState {
  company_id: string;
  company_name: string;
  employees: Member[];
  dependents: Member[];
  enrollments: Record<string, unknown>[];
  open_issues: OpenIssue[];
}

/**
 * Run describes document run identity and observed completion.
 * @brief: Run -> document run identity and observed completion
 * @returns: value [Run] -> validated server boundary
 */
export interface Run {
  id: string;
  company: string;
  filename: string;
  started_at: string;
  status: "running" | "completed" | "failed" | "unknown";
}

/**
 * AuditEvent describes original runtime event metadata and payloads.
 * @brief: AuditEvent -> original runtime event metadata and payloads
 * @returns: value [AuditEvent] -> validated server boundary
 */
export interface AuditEvent {
  [field: string]: unknown;
  run_id: string;
  sequence: number;
  timestamp: string;
  component: string;
  operation: string;
  status: string;
  elapsed_ms: number;
  input: unknown;
  output: unknown;
  input_type?: string;
  output_type?: string;
  error?: string;
}

/**
 * RunResult describes document submission result including post-commit failures.
 * @brief: RunResult -> document submission result including post-commit failures
 * @returns: value [RunResult] -> validated server boundary
 */
export interface RunResult {
  run_id: string;
  state?: CensusState;
  error?: string;
  committed?: boolean;
}

/**
 * isRecord narrows an unchecked JSON object.
 * @brief: isRecord -> excludes arrays and null
 * @params: value [unknown] -> unchecked JSON
 * @returns: valid [boolean] -> whether fields can be inspected
 */
export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * isIssue checks the issue fields rendered by the screen.
 * @brief: isIssue -> validates a server issue
 * @params: value [unknown] -> unchecked issue
 * @returns: valid [boolean] -> whether the issue contract matches
 */
function isIssue(value: unknown): value is OpenIssue {
  return (
    isRecord(value) &&
    (value.status === "OPEN" || value.status === "RESOLVED") &&
    ["id", "kind", "detail", "status"].every(
      (key) => typeof value[key] === "string",
    ) &&
    ["employee_id", "subject", "field"].every(
      (key) => value[key] == null || typeof value[key] === "string",
    ) &&
    (value.claims == null ||
      (Array.isArray(value.claims) &&
        value.claims.every((claim) => typeof claim === "string")))
  );
}

/**
 * readState validates the census and normalizes nil collections to empty lists.
 * @brief: readState -> preserves unknown fields without inventing facts
 * @params: value [unknown] -> unchecked server census
 * @returns: state [CensusState] -> validated census
 */
export function readState(value: unknown): CensusState {
  if (
    !isRecord(value) ||
    typeof value.company_id !== "string" ||
    typeof value.company_name !== "string"
  ) {
    throw new Error("The server returned an invalid census.");
  }
  const employees = value.employees ?? [];
  const dependents = value.dependents ?? [];
  const enrollments = value.enrollments ?? [];
  const issues = value.open_issues ?? [];
  if (
    !Array.isArray(employees) ||
    !employees.every(isMember) ||
    !Array.isArray(dependents) ||
    !dependents.every(isMember) ||
    !Array.isArray(enrollments) ||
    !enrollments.every(isRecord) ||
    !Array.isArray(issues) ||
    !issues.every(isIssue)
  ) {
    throw new Error("The server returned invalid census records.");
  }
  return {
    company_id: value.company_id,
    company_name: value.company_name,
    employees,
    dependents,
    enrollments,
    open_issues: issues,
  };
}

/**
 * isMember checks the identity used to label an issue.
 * @brief: isMember -> validates member display fields
 * @params: value [unknown] -> unchecked member
 * @returns: valid [boolean] -> whether the identity fields match
 */
function isMember(value: unknown): value is Member {
  return (
    isRecord(value) &&
    typeof value.id === "string" &&
    typeof value.first_name === "string" &&
    typeof value.last_name === "string"
  );
}

/**
 * readJSON reports hosting failures without leaking HTML into JSON parser errors.
 * @brief: readJSON -> checks one API response before decoding
 * @params: response [Response] -> backend or proxy response
 * @returns: value [Promise<unknown>] -> unchecked JSON body
 */
async function readJSON(response: Response): Promise<unknown> {
  const body = await response.text();
  if (response.status === 429)
    throw new Error(
      "Too many requests (HTTP 429). Wait a moment, then refresh. Saved company data has not been removed.",
    );
  if (!body.trim())
    throw new Error(
      `The backend returned an empty response (HTTP ${response.status}). Check the backend and API proxy.`,
    );
  if (body.trimStart().startsWith("<"))
    throw new Error(
      `The API returned HTML instead of JSON (HTTP ${response.status}). Check the frontend /api proxy and BACKEND_URL.`,
    );
  try {
    return JSON.parse(body);
  } catch {
    throw new Error(
      `The backend returned invalid JSON (HTTP ${response.status}).`,
    );
  }
}

/**
 * Company describes a saved company available in the selector.
 * @brief: Company -> database identity and name
 * @returns: value [Company] -> a selectable company
 */
export interface Company {
  id: string;
  name: string;
}

/**
 * listCompanies reads the database-backed company selector.
 * @brief: listCompanies -> loads saved company names
 * @params: signal [AbortSignal] -> request cancellation
 * @returns: companies [Promise<Company[]>] -> saved companies
 * @api: /companies [GET] -> existing companies
 */
export async function listCompanies(signal: AbortSignal): Promise<Company[]> {
  const value = await request("/companies", signal);
  if (
    !Array.isArray(value) ||
    !value.every(
      (item): item is Company =>
        isRecord(item) &&
        typeof item.id === "string" &&
        typeof item.name === "string",
    )
  )
    throw new Error("The server returned an invalid company list.");
  return value;
}

/**
 * request reads JSON and preserves server error messages.
 * @brief: request -> performs one cancellable API request
 * @params: path [string] -> API route
 * @params: signal [AbortSignal] -> request cancellation
 * @params: body [FormData | undefined] -> optional document submission
 * @returns: value [Promise<unknown>] -> unchecked JSON
 */
async function request(
  path: string,
  signal: AbortSignal,
  body?: FormData,
): Promise<unknown> {
  const response = await fetch(`/api${path}`, {
    method: body ? "POST" : "GET",
    body,
    signal,
    cache: "no-store",
  });
  const value: unknown = await readJSON(response);
  if (!response.ok)
    throw new Error(
      isRecord(value) && typeof value.error === "string"
        ? value.error
        : `Request failed (${response.status}).`,
    );
  return value;
}

/**
 * listRuns reads metadata without downloading each run's trace.
 * @brief: listRuns -> discovers saved and active document runs
 * @params: company [string] -> exact company name
 * @params: signal [AbortSignal] -> request cancellation
 * @returns: runs [Promise<Run[]>] -> newest first
 * @api: /runs [GET] -> existing log metadata
 */
export async function listRuns(
  company: string,
  signal: AbortSignal,
): Promise<Run[]> {
  const value = await request(
    `/runs?company=${encodeURIComponent(company)}`,
    signal,
  );
  if (!Array.isArray(value) || !value.every(isRun))
    throw new Error("The server returned an invalid run list.");
  return value;
}

/**
 * isRun validates run metadata before it enters React state.
 * @brief: isRun -> checks the run-list contract
 * @params: value [unknown] -> unchecked run
 * @returns: valid [boolean] -> whether the run matches
 */
function isRun(value: unknown): value is Run {
  return (
    isRecord(value) &&
    ["running", "completed", "failed", "unknown"].includes(
      String(value.status),
    ) &&
    ["id", "company", "filename", "started_at", "status"].every(
      (key) => typeof value[key] === "string",
    )
  );
}

/**
 * loadState reads the company's committed census.
 * @brief: loadState -> loads saved facts and issue statuses
 * @params: company [string] -> exact company name
 * @params: signal [AbortSignal] -> request cancellation
 * @returns: state [Promise<CensusState>] -> validated saved census
 * @api: /state [GET] -> current company state
 */
export async function loadState(
  company: string,
  signal: AbortSignal,
): Promise<CensusState> {
  return readState(
    await request(`/state?company=${encodeURIComponent(company)}`, signal),
  );
}

/**
 * submitDocument sends one file and retains post-commit failure information.
 * @brief: submitDocument -> submits one document to the synchronous pipeline
 * @params: company [string] -> exact company name
 * @params: file [File] -> original document
 * @params: signal [AbortSignal] -> request cancellation
 * @params: sourceSystem [string] -> original source namespace when retrying
 * @returns: result [Promise<RunResult>] -> run identity, committed state or server error
 * @api: /run [POST] -> processes one document
 */
export async function submitDocument(
  company: string,
  file: File,
  signal: AbortSignal,
  sourceSystem = "",
): Promise<RunResult> {
  const form = new FormData();
  form.set("company", company);
  form.set("file", file);
  if (sourceSystem) form.set("source_system", sourceSystem);
  const response = await fetch("/api/run", {
    method: "POST",
    body: form,
    signal,
  });
  const value: unknown = await readJSON(response);
  if (!isRecord(value) || typeof value.run_id !== "string")
    throw new Error("The server returned no run identity.");
  if (!response.ok)
    return {
      run_id: value.run_id,
      error:
        typeof value.error === "string" ? value.error : "Processing failed.",
      committed: value.committed === true,
    };
  return { run_id: value.run_id, state: readState(value.state) };
}

/**
 * RecoveredUpload contains the original document and source namespace from a trace.
 * @brief: RecoveredUpload -> unchanged retry input
 * @returns: value [RecoveredUpload] -> original file and source system
 */
interface RecoveredUpload {
  file: File;
  sourceSystem: string;
}

/**
 * recoverUpload reads the original multipart document from recorded request bytes.
 * @brief: recoverUpload -> restores a failed upload without reselecting the file
 * @params: events [AuditEvent[]] -> failed run trace
 * @params: company [string] -> active company required to match the original request
 * @returns: upload [Promise<RecoveredUpload>] -> recovered document and namespace
 */
export async function recoverUpload(
  events: AuditEvent[],
  company: string,
): Promise<RecoveredUpload> {
  const request = events.find(
    (event) => event.component === "http" && event.operation === "request",
  );
  const input = request?.input;
  if (
    !isRecord(input) ||
    input.company !== company ||
    input.body_complete !== true ||
    !isRecord(input.body) ||
    typeof input.body.bytes !== "string" ||
    !isRecord(input.headers)
  )
    throw new Error(
      "The original upload is unavailable. Please select the document again.",
    );
  const header = Object.entries(input.headers).find(
    ([name]) => name.toLowerCase() === "content-type",
  )?.[1];
  if (!Array.isArray(header) || typeof header[0] !== "string")
    throw new Error("The original upload format is unavailable.");
  const bytes = Uint8Array.from(atob(input.body.bytes), (character) =>
    character.charCodeAt(0),
  );
  const form = await new Response(bytes, {
    headers: { "Content-Type": header[0] },
  }).formData();
  if (form.get("company") !== company)
    throw new Error("The original upload belongs to another company.");
  const file = form.get("file");
  const text = form.get("text");
  const document =
    file && typeof file !== "string"
      ? file
      : typeof text === "string"
        ? new File([text], "email.txt", { type: "text/plain" })
        : undefined;
  if (!document) throw new Error("The original document is unavailable.");
  const source = form.get("source_system");
  return {
    file: document,
    sourceSystem: typeof source === "string" ? source : "",
  };
}

/**
 * removeRun hides a run without deleting its audit evidence or committed census.
 * @brief: removeRun -> removes a finished job from visible history
 * @params: id [string] -> run identity
 * @params: signal [AbortSignal] -> request cancellation
 * @returns: done [Promise<void>] -> hidden run or server error
 * @api: /runs/{id} [DELETE] -> hides one run
 */
export async function removeRun(
  id: string,
  signal: AbortSignal,
): Promise<void> {
  const response = await fetch(`/api/runs/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal,
  });
  const value = await readJSON(response);
  if (!response.ok)
    throw new Error(
      isRecord(value) && typeof value.error === "string"
        ? value.error
        : "The run could not be removed.",
    );
}

/**
 * loadTrace reads complete JSONL records while a trace is still being appended.
 * @brief: loadTrace -> returns the original ordered audit events
 * @params: id [string] -> server run ID
 * @params: signal [AbortSignal] -> request cancellation
 * @returns: events [Promise<AuditEvent[]>] -> validated events without a partial trailing record
 * @api: /logs/{id} [GET] -> exact runtime trace
 */
export async function loadTrace(
  id: string,
  signal: AbortSignal,
): Promise<AuditEvent[]> {
  const response = await fetch(
    `/api/logs/${encodeURIComponent(id)}?format=jsonl`,
    { signal, cache: "no-store" },
  );
  if (!response.ok) throw new Error(`Trace unavailable (${response.status}).`);
  const text = await response.text();
  if (text.trimStart().startsWith("<"))
    throw new Error(
      "The trace API returned HTML. Check the frontend /api proxy and BACKEND_URL.",
    );
  const lines = text.split("\n");
  if (!text.endsWith("\n")) lines.pop();
  return lines
    .filter((line) => line.trim() !== "")
    .map((line) => {
      const value: unknown = JSON.parse(line);
      if (
        !isRecord(value) ||
        typeof value.sequence !== "number" ||
        typeof value.elapsed_ms !== "number" ||
        !["run_id", "timestamp", "component", "operation", "status"].every(
          (key) => typeof value[key] === "string",
        )
      ) {
        throw new Error("The trace contains an invalid event.");
      }
      return {
        ...value,
        run_id: String(value.run_id),
        sequence: value.sequence,
        timestamp: String(value.timestamp),
        component: String(value.component),
        operation: String(value.operation),
        status: String(value.status),
        elapsed_ms: value.elapsed_ms,
        input: value.input,
        output: value.output,
        input_type:
          typeof value.input_type === "string" ? value.input_type : undefined,
        output_type:
          typeof value.output_type === "string" ? value.output_type : undefined,
        error: typeof value.error === "string" ? value.error : undefined,
      };
    });
}
