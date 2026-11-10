// Package audit records ordered, lossless payload traces for a single census run.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

/*
Event captures one boundary without replacing its payload with a summary.

* @brief: Event -> operation payloads, timing and outcome
* @returns: event [Event] -> one started, finished or failed operation
*/
type Event struct {
	Component string    `json:"component"`
	Operation string    `json:"operation"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Input     any       `json:"input"`
	Output    any       `json:"output"`
	Error     string    `json:"error,omitempty"`
}

/*
Recorder serializes events into one synchronously readable JSONL file.

* @brief: Recorder -> sequence and destination owned by one request
* @returns: recorder [Recorder] -> run-scoped trace writer
*/
type Recorder struct {
	mu          sync.Mutex
	path, runID string
	sequence    uint64
	err         error
}
type contextKey struct{}

/*
New creates the trace inside an already allocated run directory.

* @brief: New -> opens audit.jsonl for this run
* @params: dir [string] -> existing run directory
* @params: runID [string] -> run identity attached to every event
* @returns: recorder [*Recorder] -> writer for this run
* @returns: err [error] -> file creation or close failure
*/
func New(dir, runID string) (*Recorder, error) {
	path := filepath.Join(dir, "audit.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	if err = f.Close(); err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	return &Recorder{path: path, runID: runID}, nil
}

/*
WithRecorder passes the request's writer through existing context boundaries.

* @brief: WithRecorder -> attaches a run trace without global state
* @params: ctx [context.Context] -> current request context
* @params: recorder [*Recorder] -> run's writer
* @returns: traced [context.Context] -> context used by pipeline, model and SQL
*/
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	return context.WithValue(ctx, contextKey{}, recorder)
}

/*
FromContext returns the trace, or nil for callers that do not request tracing.

* @brief: FromContext -> reads the optional run writer
* @params: ctx [context.Context] -> current operation context
* @returns: recorder [*Recorder] -> writer attached by the caller
*/
func FromContext(ctx context.Context) *Recorder {
	recorder, _ := ctx.Value(contextKey{}).(*Recorder)
	return recorder
}

/*
Record flushes one event before the caller continues, including on cancellation.

* @brief: Record -> appends a timestamped and sequenced payload
* @params: ctx [context.Context] -> context carrying the writer
* @params: event [Event] -> actual operation input and output
* @returns: err [error] -> encoding, write, sync or close failure
*/
func Record(ctx context.Context, event Event) error {
	r := FromContext(ctx)
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.sequence++
	record := struct {
		Event
		RunID      string    `json:"run_id"`
		Sequence   uint64    `json:"sequence"`
		Timestamp  time.Time `json:"timestamp"`
		ElapsedMS  float64   `json:"elapsed_ms"`
		InputType  string    `json:"input_type"`
		OutputType string    `json:"output_type"`
	}{Event: event, RunID: r.runID, Sequence: r.sequence, Timestamp: time.Now().UTC(), InputType: fmt.Sprintf("%T", event.Input), OutputType: fmt.Sprintf("%T", event.Output)}
	if !event.EndedAt.IsZero() {
		record.ElapsedMS = event.EndedAt.Sub(event.StartedAt).Seconds() * 1000
	}
	body, err := json.Marshal(record)
	if err == nil {
		var file *os.File
		file, err = os.OpenFile(r.path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, writeErr := file.Write(append(body, '\n'))
			err = errors.Join(writeErr, file.Sync(), file.Close())
		}
	}
	if err != nil {
		r.err = fmt.Errorf("audit: %w", err)
	}
	return r.err
}

/*
Headers preserves header values except explicitly identified credentials.

* @brief: Headers -> copies headers with credential fields redacted
* @params: headers [http.Header] -> actual request or response headers
* @returns: redacted [http.Header] -> header copy safe for the trace
*/
func Headers(headers http.Header) http.Header {
	out := headers.Clone()
	for name := range out {
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key":
			out[name] = []string{"[redacted]"}
		}
	}
	return out
}

/*
Body preserves exact bytes in base64 and adds readable text only for valid UTF-8.

* @brief: Body -> lossless payload and byte count
* @params: body [[]byte] -> actual bytes read or written
* @returns: payload [map[string]any] -> bytes and optional text, never clipped
*/
func Body(body []byte) map[string]any {
	out := map[string]any{"bytes": body, "encoding": "base64", "byte_length": len(body)}
	if utf8.Valid(body) {
		out["text"] = string(body)
	}
	return out
}
