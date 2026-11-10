/*
Census applies one census document at a time and records every pipeline stage.

* @brief: Census -> local HTTP server that turns one document and the current company state into the next state
*/
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"census/src/audit"
	"census/src/extractor"
	"census/src/reconcile"
)

/*
server holds the database, extractor, and log service for the HTTP routes.

* @brief: server -> dependencies for the census HTTP routes
* @returns: value [server] -> database, extractor, log service, and log directory
*/
type server struct {
	db        *DB
	extractor Extractor
	logs      *LogService
	logDir    string
}

/*
errorResponse identifies a failed request and whether its state already committed.

* @brief: errorResponse -> JSON error returned by an HTTP route
* @returns: value [errorResponse] -> error, run identity, and explicit post-commit indicator
*/
type errorResponse struct {
	Error     string `json:"error"`
	Committed bool   `json:"committed,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	LogPath   string `json:"log_path,omitempty"`
}

/*
runResponse is the JSON body for a committed run.

* @brief: runResponse -> JSON body returned by a successful run
* @returns: value [runResponse] -> run id, log path, and committed state
*/
type runResponse struct {
	RunID      string            `json:"run_id"`
	IssuesPath string            `json:"issues_path"`
	LogPath    string            `json:"log_path"`
	State      State             `json:"state"`
	Summary    reconcile.Summary `json:"summary"`
}

/*
main starts the Census HTTP server and blocks until it shuts down.

* @brief: main -> starts the HTTP server and waits for SIGINT or SIGTERM
*/
func main() {
	if err := serve(); err != nil {
		fmt.Fprintf(os.Stderr, "census: %v\n", err)
		os.Exit(1)
	}
}

/*
serve opens the database, migrates it, and blocks on the HTTP server.

* @brief: serve -> opens the database and serves HTTP until shutdown
* @returns: err [error] -> non-nil when startup or the server fails
*/
func serve() error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("OPENAI_API_KEY is required")
	}
	model := envOr("OPENAI_MODEL", "gpt-6.1-sol")
	reasoningEffort := envOr("OPENAI_REASONING_EFFORT", "medium")
	migrations := envOr("MIGRATIONS_DIR", "migrations")
	logDir := envOr("LOG_DIR", "logs")
	addr := envOr("ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := reconcile.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "census: close database: %v\n", closeErr)
		}
	}()
	if err := db.Migrate(ctx, migrations); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	logs := StartLogService()
	defer logs.Stop()
	modelExtractor := extractor.NewOpenAIExtractor(apiKey, model, nil)
	modelExtractor.SetReasoningEffort(reasoningEffort)
	s := &server{
		db:        db,
		extractor: modelExtractor,
		logs:      logs,
		logDir:    logDir,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /run", s.handleRun)
	mux.HandleFunc("GET /logs/{id}", s.handleLogs)
	mux.HandleFunc("GET /runs", s.handleRuns)
	mux.HandleFunc("GET /state", s.handleState)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		err := <-serveErr
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

/*
envOr returns an environment value or fallback when the variable is empty.

* @brief: envOr -> reads one environment variable with a default
* @params: key [string] -> environment variable name
* @params: fallback [string] -> value used when the variable is empty
* @returns: value [string] -> trimmed environment value or the fallback
*/
func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

/*
handleRun accepts one company document and returns the committed state.

* @brief: handleRun -> runs one document for a company
* @params: w [http.ResponseWriter] -> JSON response writer
* @params: r [*http.Request] -> multipart request with company and file or text
* @api: /run [POST] -> accepts one document and a company name, and returns the next state
*/
func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	response := &traceResponse{ResponseWriter: w}
	originalBody := r.Body
	body, bodyErr := io.ReadAll(http.MaxBytesReader(response, originalBody, 32<<20))
	_ = originalBody.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	var company string
	var document Document
	var ok bool
	if bodyErr != nil {
		writeJSON(response, http.StatusBadRequest, errorResponse{Error: "request body exceeds limit or could not be read"})
	} else {
		company, document, ok = readRunDocument(response, r)
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	lg, err := s.logs.NewRun(s.logDir, company, document.Filename)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		fmt.Fprintf(os.Stderr, "census: HTTP trace: %v\n", err)
		return
	}
	ctx, finish, err := startHTTPTrace(r, lg, response, body, bodyErr, started)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error(), RunID: lg.ID(), LogPath: lg.Path()})
		fmt.Fprintf(os.Stderr, "census: HTTP trace: %v\n", err)
		return
	}
	defer finish()
	if !ok {
		return
	}
	state, err := Run(ctx, company, document, s.db, s.extractor, lg)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, errorResponse{Error: err.Error(), RunID: lg.ID(), LogPath: lg.Path(), Committed: state.CompanyID != ""})
		return
	}
	writeJSON(response, http.StatusOK, runResponse{
		RunID: lg.ID(), LogPath: lg.Path(), IssuesPath: "/logs/" + lg.ID() + "?format=issues", State: state, Summary: reconcile.Summarize(state),
	})
}

/*
readRunDocument reads the company and either the upload or the email text.

* @brief: readRunDocument -> reads the multipart company and document
* @params: w [http.ResponseWriter] -> response writer used for a 400 body
* @params: r [*http.Request] -> request whose form holds company, file, and text
* @returns: company [string] -> trimmed company name
* @returns: document [Document] -> uploaded file or email body
* @returns: ok [bool] -> false when the response already describes a 400
*/
func readRunDocument(w http.ResponseWriter, r *http.Request) (string, Document, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid multipart form"})
		return "", Document{}, false
	}
	company := strings.TrimSpace(r.FormValue("company"))
	if company == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "company is required"})
		return "", Document{}, false
	}
	file, header, fileErr := r.FormFile("file")
	text := r.FormValue("text")
	if fileErr != nil && strings.TrimSpace(text) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "file or text is required"})
		return "", Document{}, false
	}
	if fileErr == nil {
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "file could not be read"})
			return "", Document{}, false
		}
		contentType := header.Header.Get("Content-Type")
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/octet-stream"
		}
		return company, Document{
			Filename:     header.Filename,
			SourceSystem: strings.TrimSpace(r.FormValue("source_system")),
			ContentType:  contentType,
			Body:         body,
		}, true
	}
	return company, Document{
		Filename:     "email.txt",
		SourceSystem: strings.TrimSpace(r.FormValue("source_system")),
		ContentType:  "text/plain",
		Body:         []byte(text),
	}, true
}

/*
handleLogs returns the step files for one named run, in file order.

* @brief: handleLogs -> concatenates the step files in one run folder
* @params: w [http.ResponseWriter] -> response writer
* @params: r [*http.Request] -> request whose path contains the run folder name
* @api: /logs/{id} [GET] -> returns read, parse, extract, format, map, write, and latency
*/
func (s *server) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid log id"})
		return
	}
	dir := filepath.Join(s.logDir, id)
	format := r.URL.Query().Get("format")
	if format == "issues" {
		body, err := os.ReadFile(filepath.Join(dir, "issues.txt"))
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "issue report could not be read"})
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
		return
	}
	if format != "text" {
		body, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
		if err == nil {
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write(body)
			return
		}
		if !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "trace could not be read"})
			return
		}
		if format == "jsonl" {
			http.NotFound(w, r)
			return
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "log could not be read"})
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		names = append(names, entry.Name())
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			fmt.Fprintf(w, "== %s unreadable ==\n", name)
			continue
		}
		_, _ = w.Write(body)
		if len(body) == 0 || body[len(body)-1] != '\n' {
			_, _ = w.Write([]byte("\n"))
		}
	}
}

/*
handleState returns the saved census for one company.

* @brief: handleState -> loads and returns one company census
* @params: w [http.ResponseWriter] -> JSON response writer
* @params: r [*http.Request] -> request whose query contains company
* @api: /state [GET] -> returns the saved employees, dependents, enrollments, and open issues
*/
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	body, bodyErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	_ = r.Body.Close()
	company := strings.TrimSpace(r.URL.Query().Get("company"))
	lg, err := s.logs.NewRun(s.logDir, company, "state")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	response := &traceResponse{ResponseWriter: w}
	ctx, finish, err := startHTTPTrace(r, lg, response, body, bodyErr, started)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error(), RunID: lg.ID(), LogPath: lg.Path()})
		return
	}
	defer finish()
	w = response
	if bodyErr != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "request body exceeds limit or could not be read"})
		return
	}
	if company == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "company is required"})
		return
	}
	unlock := s.db.LockCompany(company)
	if unlock != nil {
		defer unlock()
	}
	state, err := s.db.Load(ctx, company)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

/*
writeJSON writes a JSON response with the given status code.

* @brief: writeJSON -> encodes a JSON response
* @params: w [http.ResponseWriter] -> response writer
* @params: status [int] -> HTTP status code
* @params: value [any] -> body encoded as JSON
*/
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// traceResponse buffers the encoded response until its trace is durable.
type traceResponse struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
	header http.Header
}

/*
WriteHeader captures the status and headers before tracing the response.

* @brief: WriteHeader -> records the actual response status and headers
* @params: status [int] -> response status code
*/
func (w *traceResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.header = w.Header().Clone()
}

/*
Write buffers the encoded payload until the response trace is durable.

* @brief: Write -> buffers HTTP response bytes
* @params: body [[]byte] -> encoded response payload
* @returns: n [int] -> bytes buffered
* @returns: err [error] -> buffer write error
*/
func (w *traceResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(body)
}

/*
startHTTPTrace attaches the run recorder and captures transport boundaries.

* @brief: startHTTPTrace -> records a request and returns response finalization
* @params: r [*http.Request] -> incoming HTTP metadata
* @params: lg [*RunLog] -> allocated run folder
* @params: w [*traceResponse] -> actual response capture
* @params: body [[]byte] -> original bounded request body
* @params: bodyErr [error] -> body read or size error
* @params: started [time.Time] -> time request handling began
* @returns: ctx [context.Context] -> request context carrying the recorder
* @returns: finish [func()] -> records the completed response
* @returns: err [error] -> recorder creation or request recording failure
*/
func startHTTPTrace(r *http.Request, lg *RunLog, w *traceResponse, body []byte, bodyErr error, started time.Time) (context.Context, func(), error) {
	recorder, err := audit.New(lg.Path(), lg.ID())
	if err != nil {
		return r.Context(), nil, err
	}
	ctx := audit.WithRecorder(r.Context(), recorder)
	header := audit.Headers(r.Header)
	input := map[string]any{
		"method": r.Method, "endpoint": r.URL.Path, "request_uri": r.RequestURI,
		"protocol": r.Proto, "host": r.Host, "headers": header, "content_length": r.ContentLength,
		"body": audit.Body(body), "body_complete": bodyErr == nil,
		"redacted_headers": []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "Api-Key"},
	}
	if r.URL.Path == "/run" {
		input["company"] = strings.TrimSpace(r.FormValue("company"))
		input["filename"] = "email.txt"
		if r.MultipartForm != nil && len(r.MultipartForm.File["file"]) > 0 {
			input["filename"] = r.MultipartForm.File["file"][0].Filename
		}
	}
	event := audit.Event{Component: "http", Operation: "request", Status: "ok", StartedAt: started, EndedAt: time.Now(), Input: input}
	if bodyErr != nil {
		event.Status = "error"
		event.Error = bodyErr.Error()
	}
	if err := audit.Record(ctx, event); err != nil {
		return ctx, nil, err
	}
	w.Header().Set("X-Run-ID", lg.ID())
	if w.header != nil {
		w.header.Set("X-Run-ID", lg.ID())
	}
	return ctx, func() {
		event := audit.Event{
			Component: "http", Operation: "response_ready", Status: "ready", StartedAt: started, EndedAt: time.Now(),
			Output: map[string]any{"status": w.status, "headers": audit.Headers(w.header), "body": audit.Body(w.body.Bytes())},
		}
		if w.status >= 400 {
			event.Status = "error"
		}
		if err := audit.Record(ctx, event); err != nil {
			var result errorResponse
			_ = json.Unmarshal(w.body.Bytes(), &result)
			committed := result.Committed || (r.URL.Path == "/run" && w.status == http.StatusOK)
			writeJSON(w.ResponseWriter, http.StatusInternalServerError, errorResponse{
				Error: "response trace failed: " + err.Error(), RunID: lg.ID(), LogPath: lg.Path(), Committed: committed,
			})
			return
		}
		writeStarted := time.Now()
		w.ResponseWriter.WriteHeader(w.status)
		n, writeErr := w.ResponseWriter.Write(w.body.Bytes())
		transport := audit.Event{
			Component: "http", Operation: "transport_write", Status: "ok", StartedAt: writeStarted, EndedAt: time.Now(),
			Output: map[string]any{"status": w.status, "headers": audit.Headers(w.header), "accepted_body": audit.Body(w.body.Bytes()[:n]), "total_elapsed_ms": time.Since(started).Seconds() * 1000},
		}
		if writeErr != nil {
			transport.Status = "error"
			transport.Error = writeErr.Error()
		}
		if err := audit.Record(ctx, transport); err != nil {
			fmt.Fprintf(os.Stderr, "census: record HTTP transport for %s: %v\n", lg.ID(), err)
		}
		if err := audit.Record(ctx, audit.Event{Component: "http", Operation: "completion", Status: event.Status, StartedAt: started, EndedAt: time.Now(), Output: map[string]any{"status": w.status}}); err != nil {
			fmt.Fprintf(os.Stderr, "census: record HTTP completion for %s: %v\n", lg.ID(), err)
		}
	}, nil
}
