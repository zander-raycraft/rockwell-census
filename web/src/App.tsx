import { useEffect, useRef, useState } from "react";
import type { DragEvent, FormEvent } from "react";
import {
  listCompanies,
  recoverUpload,
  removeRun,
  listRuns,
  loadState,
  loadTrace,
  submitDocument,
} from "./api";
import type { AuditEvent, CensusState, Company, Run } from "./api";
import { AuditView, IssuesView } from "./AuditView";
import { CensusView } from "./CensusView";

/**
 * errorText keeps unexpected request failures readable.
 * @brief: errorText -> formats an unchecked exception
 * @params: error [unknown] -> request failure
 * @returns: message [string] -> user-visible error
 */
function errorText(error: unknown): string {
  return error instanceof Error ? error.message : "The request failed.";
}

/**
 * App connects the document controls to the existing census pipeline and traces.
 * @brief: App -> manages one company, sequential uploads and read-only observability
 * @returns: view [React.JSX.Element] -> document, run and issue workspace
 */
export function App() {
  const [companies, setCompanies] = useState<Company[]>([]);
  const [companyError, setCompanyError] = useState("");
  const [companyInput, setCompanyInput] = useState("");
  const [company, setCompany] = useState("");
  const [feedback, setFeedback] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  const [isEditingRun, setIsEditingRun] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [uploadStatus, setUploadStatus] = useState("");
  const [runs, setRuns] = useState<Run[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [historyEvents, setHistoryEvents] = useState<AuditEvent[]>([]);
  const [state, setState] = useState<CensusState>();
  const [tab, setTab] = useState("process");
  const [error, setError] = useState("");
  const [stateError, setStateError] = useState("");
  const [runError, setRunError] = useState("");
  const [traceError, setTraceError] = useState("");
  const [historyError, setHistoryError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const dragDepth = useRef(0);
  const pendingRun = useRef<
    { filename: string; knownIDs: Set<string> } | undefined
  >(undefined);
  const uploadController = useRef<AbortController | undefined>(undefined);
  const selectedRun = runs.find((run) => run.id === selectedID);
  const latestFinished =
    runs.find((run) => run.status === "completed" || run.status === "failed")
      ?.id ?? "";

  useEffect(() => {
    const controller = new AbortController();
    /**
     * refreshCompanies discovers saved companies when the workspace opens or refreshes.
     * @brief: refreshCompanies -> updates the selector from the database
     * @returns: done [Promise<void>] -> company list loaded or error displayed
     */
    async function refreshCompanies(): Promise<void> {
      try {
        const next = await listCompanies(controller.signal);
        if (!controller.signal.aborted) {
          setCompanies(next);
          setCompanyError("");
        }
      } catch (failure) {
        if (!controller.signal.aborted) setCompanyError(errorText(failure));
      }
    }
    void refreshCompanies();
    return () => controller.abort();
  }, [refresh]);

  useEffect(() => {
    return () => uploadController.current?.abort();
  }, []);

  useEffect(() => {
    if (!company) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    /**
     * pollRuns discovers runs while uploads are still awaiting their response.
     * @brief: pollRuns -> refreshes metadata without overlapping requests
     * @returns: done [Promise<void>] -> one polling cycle
     */
    async function pollRuns(): Promise<void> {
      let shouldPoll = isSubmitting;
      try {
        const next = await listRuns(company, controller.signal);
        if (controller.signal.aborted) return;
        shouldPoll ||= next.some((run) => run.status === "running");
        setRuns(next);
        const discovered = next.find(
          (run) =>
            pendingRun.current &&
            run.filename === pendingRun.current.filename &&
            !pendingRun.current.knownIDs.has(run.id),
        );
        if (discovered) {
          setSelectedID(discovered.id);
          pendingRun.current = undefined;
        }
        setRunError("");
        setSelectedID((current) => current || next[0]?.id || "");
      } catch (failure) {
        if (!controller.signal.aborted) setRunError(errorText(failure));
      } finally {
        if (!controller.signal.aborted && shouldPoll)
          timer = setTimeout(pollRuns, 2000);
      }
    }
    void pollRuns();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [company, refresh, isSubmitting]);

  useEffect(() => {
    if (!company) return;
    const controller = new AbortController();
    /**
     * refreshState keeps the displayed census tied to committed server data.
     * @brief: refreshState -> reloads state and its latest available history
     * @returns: done [Promise<void>] -> state and history retrieval
     */
    async function refreshState(): Promise<void> {
      try {
        const next = await loadState(company, controller.signal);
        if (!controller.signal.aborted) {
          setState(next);
          setCompanies((current) =>
            current.some((saved) => saved.id === next.company_id)
              ? current
              : [...current, { id: next.company_id, name: next.company_name }],
          );
          setStateError("");
        }
      } catch (failure) {
        if (!controller.signal.aborted) setStateError(errorText(failure));
      }
      if (!latestFinished || controller.signal.aborted) return;
      try {
        const next = await loadTrace(latestFinished, controller.signal);
        if (!controller.signal.aborted) {
          setHistoryEvents(next);
          setHistoryError("");
        }
      } catch (failure) {
        if (!controller.signal.aborted) setHistoryError(errorText(failure));
      }
    }
    void refreshState();
    return () => controller.abort();
  }, [company, latestFinished, refresh]);

  useEffect(() => {
    setEvents([]);
    setTraceError("");
    if (!selectedID) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    /**
     * pollTrace retrieves completed records from the selected trace.
     * @brief: pollTrace -> polls active runs and loads finished runs once
     * @returns: done [Promise<void>] -> one trace retrieval
     */
    async function pollTrace(): Promise<void> {
      try {
        const next = await loadTrace(selectedID, controller.signal);
        if (controller.signal.aborted) return;
        setEvents(next);
        setTraceError("");
      } catch (failure) {
        if (!controller.signal.aborted) setTraceError(errorText(failure));
      } finally {
        if (!controller.signal.aborted && selectedRun?.status === "running")
          timer = setTimeout(pollTrace, 1500);
      }
    }
    void pollTrace();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [selectedID, selectedRun?.status, refresh]);

  /**
   * openCompany discards pending responses before showing another company.
   * @brief: openCompany -> switches the workspace to an exact company name
   * @params: event [FormEvent<HTMLFormElement>] -> company form submission
   * @returns: result [void] -> updates active company
   */
  function openCompany(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    switchCompany(companyInput.trim());
  }

  /**
   * switchCompany cancels old requests before loading another company.
   * @brief: switchCompany -> opens one exact company name
   * @params: next [string] -> saved or newly entered company name
   * @returns: result [void] -> reset workspace for the selected company
   */
  function switchCompany(next: string): void {
    if (!next) return;
    uploadController.current?.abort();
    pendingRun.current = undefined;
    setIsSubmitting(false);
    setUploadStatus("");
    setCompany(next);
    setCompanyInput(next);
    setFeedback("");
    setRuns([]);
    setSelectedID("");
    setState(undefined);
    setHistoryEvents([]);
    setHistoryError("");
    setStateError("");
    setRunError("");
    setError("");
    setRefresh((value) => value + 1);
  }

  /**
   * addFiles appends a batch while suppressing repeated selections.
   * @brief: addFiles -> retains original File objects until explicit submission
   * @params: incoming [File[]] -> picked or dropped files
   * @returns: result [void] -> updates the file selection
   */
  function addFiles(incoming: File[]): void {
    setFiles((current) => {
      const next = [...current];
      for (const file of incoming)
        if (
          !next.some(
            (saved) =>
              saved.name === file.name &&
              saved.size === file.size &&
              saved.lastModified === file.lastModified,
          )
        )
          next.push(file);
      return next;
    });
  }

  /**
   * dropFiles ends the drag state and keeps the batch local until submission.
   * @brief: dropFiles -> selects dropped documents
   * @params: event [DragEvent<HTMLElement>] -> dropped browser files
   * @returns: result [void] -> updates file selection
   */
  function dropFiles(event: DragEvent<HTMLElement>): void {
    event.preventDefault();
    dragDepth.current = 0;
    setIsDragging(false);
    if (!isSubmitting) addFiles(Array.from(event.dataTransfer.files));
  }

  /**
   * processFiles submits the batch in order so later documents see earlier commits.
   * @brief: processFiles -> runs one request at a time and stops on failure
   * @params: batch [File[]] -> documents in processing order
   * @params: sourceSystem [string] -> retained source namespace for a retry
   * @returns: completed [Promise<boolean>] -> whether every document succeeded
   */
  async function processFiles(
    batch: File[],
    sourceSystem = "",
  ): Promise<boolean> {
    if (!company || isSubmitting || batch.length === 0) return false;
    const controller = new AbortController();
    uploadController.current = controller;
    setIsSubmitting(true);
    setError("");
    setTab("process");
    setSelectedID("");
    let completed = true;
    for (const [index, file] of batch.entries()) {
      if (controller.signal.aborted) return false;
      setUploadStatus(`${index + 1} / ${batch.length} · ${file.name}`);
      pendingRun.current = {
        filename: file.name,
        knownIDs: new Set(runs.map((run) => run.id)),
      };
      try {
        const result = await submitDocument(
          company,
          file,
          controller.signal,
          sourceSystem,
        );
        if (controller.signal.aborted) return false;
        pendingRun.current = undefined;
        setSelectedID(result.run_id);
        setRefresh((value) => value + 1);
        if (result.state) setState(result.state);
        if (result.error) {
          setError(
            `${result.error}${result.committed ? " Data was committed; inspect this run before retrying." : ""}`,
          );
          completed = false;
          break;
        }
        setFiles((current) => current.filter((saved) => saved !== file));
        if (index < batch.length - 1) setSelectedID("");
      } catch (failure) {
        if (controller.signal.aborted) return false;
        setError(
          `${errorText(failure)} Check the run history before retrying; the server may still have processed the document.`,
        );
        completed = false;
        break;
      }
    }
    if (!controller.signal.aborted) {
      pendingRun.current = undefined;
      setIsSubmitting(false);
      setUploadStatus("");
      setRefresh((value) => value + 1);
    }
    return completed && !controller.signal.aborted;
  }

  /**
   * retrySelected restores a failed job's document and creates a new processing run.
   * @brief: retrySelected -> retries the original input against current company data
   * @returns: done [Promise<void>] -> retry started or retrieval error displayed
   */
  async function retrySelected(): Promise<void> {
    if (isSubmitting || isEditingRun) return;
    setIsEditingRun(true);
    const controller = new AbortController();
    uploadController.current = controller;
    try {
      const original = await recoverUpload(events, company);
      if (controller.signal.aborted) return;
      await processFiles([original.file], original.sourceSystem);
    } catch (failure) {
      if (!controller.signal.aborted) setError(errorText(failure));
    } finally {
      setIsEditingRun(false);
    }
  }

  /**
   * removeSelected hides a finished run for this company while retaining audit evidence.
   * @brief: removeSelected -> removes the selected history entry
   * @returns: done [Promise<void>] -> removed entry or displayed failure
   */
  async function removeSelected(): Promise<void> {
    if (isSubmitting || isEditingRun || !selectedID) return;
    const id = selectedID;
    const controller = new AbortController();
    uploadController.current = controller;
    setIsEditingRun(true);
    try {
      await removeRun(id, controller.signal);
      if (controller.signal.aborted) return;
      setRuns((current) => current.filter((run) => run.id !== id));
      setSelectedID("");
      setRefresh((value) => value + 1);
    } catch (failure) {
      if (!controller.signal.aborted) setError(errorText(failure));
    } finally {
      setIsEditingRun(false);
    }
  }

  /**
   * sendFeedback submits a plain-text reply as an email document.
   * @brief: sendFeedback -> reuses extraction, reconciliation and run history
   * @params: event [FormEvent<HTMLFormElement>] -> reply form submission
   * @returns: done [Promise<void>] -> reply processed or retained after failure
   */
  async function sendFeedback(
    event: FormEvent<HTMLFormElement>,
  ): Promise<void> {
    event.preventDefault();
    if (!feedback.trim()) return;
    const reply = feedback;
    const file = new File([reply], "email.txt", { type: "text/plain" });
    if (await processFiles([file])) setFeedback("");
  }

  /**
   * showRun switches from issue history to its original processing trace.
   * @brief: showRun -> opens a referenced run
   * @params: id [string] -> persisted run identity
   * @returns: result [void] -> selects the run and process tab
   */
  function showRun(id: string): void {
    setSelectedID(id);
    setTab("process");
  }

  return (
    <div className="app">
      <header className="header">
        <span className="wordmark">Rockwell</span>
        <div className="company-picker">
          <label htmlFor="saved-company">Switch company</label>
          <select
            id="saved-company"
            value={company}
            onChange={(event) => switchCompany(event.target.value)}
          >
            <option value="">Choose a company</option>
            {companies.map((saved) => (
              <option key={saved.id} value={saved.name}>
                {saved.name}
              </option>
            ))}
          </select>
        </div>
        <form className="company-form" onSubmit={openCompany}>
          <label htmlFor="company">Company</label>
          <input
            id="company"
            value={companyInput}
            onChange={(event) => setCompanyInput(event.target.value)}
            placeholder="Company name"
            required
          />
          <button className="quiet-button" type="submit">
            Open company
          </button>
        </form>
      </header>
      <main className="workspace">
        <aside className="sidebar">
          <section
            className={`dropzone${isDragging ? " dragging" : ""}`}
            aria-label="Document upload"
            onDragEnter={(event) => {
              event.preventDefault();
              dragDepth.current += 1;
              if (!isSubmitting) setIsDragging(true);
            }}
            onDragOver={(event) => {
              event.preventDefault();
              event.dataTransfer.dropEffect = isSubmitting ? "none" : "copy";
            }}
            onDragLeave={(event) => {
              event.preventDefault();
              dragDepth.current -= 1;
              if (dragDepth.current === 0) setIsDragging(false);
            }}
            onDrop={dropFiles}
          >
            <span className="upload-symbol" aria-hidden="true">
              ↑
            </span>
            <h2>{isDragging ? "Drop your documents" : "Upload documents"}</h2>
            <button
              className="browse-button"
              disabled={isSubmitting}
              onClick={() => input.current?.click()}
            >
              Browse files ↗
            </button>
            <input
              ref={input}
              type="file"
              multiple
              hidden
              aria-label="Choose documents"
              onChange={(event) => {
                addFiles(Array.from(event.target.files ?? []));
                event.target.value = "";
              }}
            />
          </section>
          {files.length > 0 && (
            <section className="selection" aria-label="Selected files">
              <div className="selection-heading">
                <h2>{files.length} selected</h2>
                <button
                  className="text-button"
                  disabled={isSubmitting}
                  onClick={() => setFiles([])}
                >
                  Clear
                </button>
              </div>
              <ul>
                {files.map((file) => (
                  <li key={`${file.name}-${file.size}-${file.lastModified}`}>
                    <span className="file-details">
                      <span className="file-name">{file.name}</span>
                      <small>{file.size.toLocaleString()} bytes</small>
                    </span>
                    <button
                      className="remove-button"
                      disabled={isSubmitting}
                      aria-label={`Remove ${file.name}`}
                      onClick={() =>
                        setFiles((current) =>
                          current.filter((saved) => saved !== file),
                        )
                      }
                    >
                      ×
                    </button>
                  </li>
                ))}
              </ul>
              <button
                className="browse-button process-button"
                disabled={!company || isSubmitting}
                onClick={() => void processFiles([...files])}
              >
                {isSubmitting ? "Processing…" : "Process documents"}
              </button>
              {!company && (
                <p className="muted">Open a company to process these files.</p>
              )}
            </section>
          )}
          <p className="upload-status" role="status">
            {uploadStatus}
          </p>
          <div className="sidebar-heading">
            <h2>Run history</h2>
            <span>{runs.length}</span>
          </div>
          {runError && (
            <p className="error" role="alert">
              {runError}
            </p>
          )}
          {runs.length === 0 && (
            <p className="empty">
              {company
                ? "No document runs yet."
                : "Open a company to see its runs."}
            </p>
          )}
          <nav className="run-list" aria-label="Run history">
            {runs.map((run) => (
              <button
                key={run.id}
                className={`run-item ${selectedID === run.id ? "selected" : ""}`}
                onClick={() => showRun(run.id)}
                aria-current={selectedID === run.id ? "true" : undefined}
              >
                <span className="run-filename">{run.filename}</span>
                <small>{new Date(run.started_at).toLocaleString()}</small>
                <span className={`badge ${run.status}`}>
                  {run.status === "unknown"
                    ? "No completion recorded"
                    : run.status}
                </span>
              </button>
            ))}
          </nav>
        </aside>
        <section className="content">
          <div className="content-heading">
            <div>
              <h1>{company || "Documents"}</h1>
              {state && (
                <p>
                  {state.employees.length} employees · {state.dependents.length}{" "}
                  dependents · {state.enrollments.length} enrollments
                </p>
              )}
            </div>
            <button
              className="text-button"
              disabled={!company}
              onClick={() => setRefresh((value) => value + 1)}
            >
              Refresh
            </button>
          </div>
          <div className="tabs" role="tablist" aria-label="Company views">
            {["process", "issues", "census"].map((name) => (
              <button
                key={name}
                role="tab"
                id={`tab-${name}`}
                aria-selected={tab === name}
                aria-controls={`panel-${name}`}
                onClick={() => setTab(name)}
              >
                {name === "process"
                  ? "Process"
                  : name === "issues"
                    ? `Issues${state ? ` · ${state.open_issues.filter((issue) => issue.status === "OPEN").length}` : ""}`
                    : "Saved census"}
              </button>
            ))}
          </div>
          {companyError && (
            <p className="error" role="alert">
              Company list: {companyError}
            </p>
          )}
          {stateError && (
            <p className="error" role="alert">
              {stateError}
            </p>
          )}
          {error && (
            <p className="error" role="alert">
              {error}
            </p>
          )}
          <div
            role="tabpanel"
            id={`panel-${tab}`}
            aria-labelledby={`tab-${tab}`}
          >
            {tab === "process" &&
              (selectedID ? (
                <details className="job-card" key={selectedID} open>
                  <summary className="job-summary">
                    <span>
                      <strong>{selectedRun?.filename || selectedID}</strong>
                      <small>
                        {selectedRun
                          ? new Date(selectedRun.started_at).toLocaleString()
                          : selectedID}
                      </small>
                    </span>
                    <span className={`badge ${selectedRun?.status || ""}`}>
                      {selectedRun?.status || "Loading"}
                    </span>
                  </summary>
                  <div className="job-body">
                    <div className="job-actions">
                      {selectedRun?.status === "failed" && (
                        <button
                          className="text-button"
                          aria-label="Retry run"
                          title="Rerun the original document as a new job"
                          disabled={
                            isSubmitting ||
                            isEditingRun ||
                            events.length === 0 ||
                            events.some(
                              (event) =>
                                (event.component === "pipeline" &&
                                  event.operation === "WRITE" &&
                                  event.status === "finished") ||
                                (event.component === "database" &&
                                  event.operation === "commit" &&
                                  event.status === "completed"),
                            )
                          }
                          onClick={() => void retrySelected()}
                        >
                          <span aria-hidden="true">↻</span> Retry
                        </button>
                      )}
                      <button
                        className="text-button"
                        aria-label="Remove run from history"
                        title="Hide this job from history. Saved census and audit evidence stay intact."
                        disabled={
                          isSubmitting ||
                          isEditingRun ||
                          !selectedRun ||
                          selectedRun.status === "running"
                        }
                        onClick={() => void removeSelected()}
                      >
                        <span aria-hidden="true">×</span> Remove from history
                      </button>
                    </div>
                    <AuditView
                      events={events}
                      status={selectedRun?.status || ""}
                      error={traceError}
                    />
                    <a
                      className="text-button"
                      href={`/api/logs/${encodeURIComponent(selectedID)}?format=jsonl`}
                      download={`${selectedID}.jsonl`}
                    >
                      Download original trace ↗
                    </a>
                  </div>
                </details>
              ) : (
                <p className="empty">
                  {isSubmitting
                    ? "Uploading document; waiting for its run trace…"
                    : "Select a run to inspect its data flow, or upload documents to begin."}
                </p>
              ))}
            {tab === "issues" && (
              <>
                <form
                  className="feedback-form"
                  onSubmit={(event) => void sendFeedback(event)}
                >
                  <label htmlFor="feedback">Reply with a correction</label>
                  <textarea
                    id="feedback"
                    rows={3}
                    value={feedback}
                    onChange={(event) => setFeedback(event.target.value)}
                    disabled={isSubmitting}
                    placeholder="For example: Alex Smith and Alexander Smith are the same person. Their correct SSN is…"
                    required
                  />
                  <div>
                    <span className="muted">
                      Processed as an email reply and recorded as a new job.
                    </span>
                    <button
                      className="browse-button"
                      type="submit"
                      disabled={!company || isSubmitting || !feedback.trim()}
                    >
                      Submit reply
                    </button>
                  </div>
                </form>
                <IssuesView
                  state={state}
                  events={historyEvents}
                  historyError={historyError}
                  onRun={showRun}
                />
              </>
            )}
            {tab === "census" && <CensusView state={state} />}
          </div>
        </section>
      </main>
    </div>
  );
}
