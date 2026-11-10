package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"census/src/audit"
)

// sqlConnection is implemented by the pool and an open transaction.
type sqlConnection interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type sqlValue struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

/*
sqlValues preserves SQL argument and result types, including byte slices encoded as base64 by JSON.

* @brief: sqlValues -> pairs values with their Go types
* @params: values []any -> actual arguments or scanned SQL values
* @returns: typed []sqlValue -> ordered values and Go types
*/
func sqlValues(values []any) []sqlValue {
	typed := make([]sqlValue, len(values))
	for i, value := range values {
		typed[i] = sqlValue{Type: fmt.Sprintf("%T", value), Value: value}
	}
	return typed
}

/*
sqlEvent records one actual database operation without including connection credentials.

* @brief: sqlEvent -> records SQL arguments, results, timing and transaction scope
* @params: ctx [context.Context] -> caller's trace context
* @params: connection [sqlConnection] -> pool or transaction used for this operation
* @params: operation [string] -> database operation name
* @params: status [string] -> started, completed or error
* @params: started [time.Time] -> operation start
* @params: query [string] -> exact SQL statement
* @params: args []any -> exact bound values
* @params: output [any] -> actual operation result
* @params: cause [error] -> database error, when present
* @returns: err [error] -> trace write failure
*/
func sqlEvent(ctx context.Context, connection sqlConnection, operation, status string, started time.Time, query string, args []any, output any, cause error) error {
	_, transactional := connection.(*sql.Tx)
	input := map[string]any{"sql": query, "parameters": sqlValues(args), "transactional": transactional}
	if transactional {
		input["transaction_id"] = fmt.Sprintf("%p", connection)
		input["transaction_state"] = "pending"
	}
	event := audit.Event{
		Component: "database", Operation: operation, Status: status,
		StartedAt: started, EndedAt: time.Now(),
		Input:  input,
		Output: output,
	}
	if status == "started" {
		event.EndedAt = time.Time{}
	}
	if cause != nil {
		event.Status = "error"
		event.Error = cause.Error()
	}
	return audit.Record(ctx, event)
}

/*
traceExec records the statement before executing it and its affected-row count afterward.

* @brief: traceExec -> executes and traces one SQL statement
* @params: ctx [context.Context] -> cancellation and trace context
* @params: connection [sqlConnection] -> actual pool or transaction
* @params: query [string] -> SQL statement
* @params: args []any -> bound arguments
* @returns: result [sql.Result] -> database execution result
* @returns: err [error] -> execution or trace error
*/
func traceExec(ctx context.Context, connection sqlConnection, query string, args ...any) (sql.Result, error) {
	started := time.Now()
	if err := sqlEvent(ctx, connection, "exec", "started", started, query, args, nil, nil); err != nil {
		return nil, err
	}
	started = time.Now()
	result, err := connection.ExecContext(ctx, query, args...)
	var output any
	if err == nil {
		var affected int64
		affected, err = result.RowsAffected()
		output = map[string]any{"rows_affected": affected}
	}
	return result, errors.Join(err, sqlEvent(ctx, connection, "exec", "completed", started, query, args, output, err))
}

type tracedRows struct {
	*sql.Rows
	connection sqlConnection
	query      string
	args       []any
	columns    []string
	started    time.Time
	err        error
}

/*
traceQuery records query latency and keeps the actual result set for scan tracing.

* @brief: traceQuery -> executes and traces a SQL query
* @params: ctx [context.Context] -> cancellation and trace context
* @params: connection [sqlConnection] -> actual pool or transaction
* @params: query [string] -> SQL query
* @params: args []any -> bound arguments
* @returns: rows [*tracedRows] -> actual rows with traced scans
* @returns: err [error] -> query or trace error
*/
func traceQuery(ctx context.Context, connection sqlConnection, query string, args ...any) (*tracedRows, error) {
	started := time.Now()
	if err := sqlEvent(ctx, connection, "query", "started", started, query, args, nil, nil); err != nil {
		return nil, err
	}
	started = time.Now()
	rows, err := connection.QueryContext(ctx, query, args...)
	var columns []string
	if err == nil {
		columns, err = rows.Columns()
	}
	err = errors.Join(err, sqlEvent(ctx, connection, "query", "completed", started, query, args, map[string]any{"columns": columns}, err))
	if err != nil {
		if rows != nil {
			err = errors.Join(err, rows.Close())
		}
		return nil, err
	}
	return &tracedRows{Rows: rows, connection: connection, query: query, args: args, columns: columns, started: started}, nil
}

/*
Scan records raw driver values before applying the caller's ordinary typed scan.

* @brief: Scan -> traces exact returned values and scans the current row
* @params: ctx [context.Context] -> caller's trace context
* @params: dest []any -> caller's typed scan destinations
* @returns: err [error] -> scan or trace error
*/
func (rows *tracedRows) Scan(ctx context.Context, dest ...any) error {
	started := time.Now()
	values := make([]any, len(rows.columns))
	raw := make([]any, len(values))
	for i := range values {
		raw[i] = &values[i]
	}
	err := rows.Rows.Scan(raw...)
	if err == nil {
		err = rows.Rows.Scan(dest...)
	}
	output := map[string]any{"columns": rows.columns, "values": sqlValues(values)}
	rows.err = errors.Join(rows.err, err, sqlEvent(ctx, rows.connection, "scan", "completed", started, rows.query, rows.args, output, err))
	return rows.err
}

/*
Err reports iteration and audit failures, including time spent consuming the result set.

* @brief: Err -> records result-set completion
* @params: ctx [context.Context] -> caller's trace context
* @returns: err [error] -> iteration or trace error
*/
func (rows *tracedRows) Err(ctx context.Context) error {
	err := errors.Join(rows.err, rows.Rows.Err())
	return errors.Join(err, sqlEvent(ctx, rows.connection, "rows", "completed", rows.started, rows.query, rows.args, nil, err))
}

/*
traceQueryRow preserves QueryRow's first-row semantics while retaining SQL column names and values.

* @brief: traceQueryRow -> queries and scans one traced row
* @params: ctx [context.Context] -> cancellation and trace context
* @params: connection [sqlConnection] -> actual pool or transaction
* @params: query [string] -> SQL query
* @params: args []any -> bound arguments
* @params: dest []any -> typed scan destinations
* @returns: err [error] -> query, scan or trace error
*/
func traceQueryRow(ctx context.Context, connection sqlConnection, query string, args []any, dest ...any) (err error) {
	rows, err := traceQuery(ctx, connection, query, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err := rows.Err(ctx); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return errors.Join(rows.Scan(ctx, dest...), rows.Err(ctx))
}

/*
traceBegin records transaction start and rolls back if its completed trace cannot be written.

* @brief: traceBegin -> begins one traced transaction
* @params: ctx [context.Context] -> cancellation and trace context
* @params: pool [*sql.DB] -> actual database pool
* @returns: tx [*sql.Tx] -> transaction ready for statements
* @returns: err [error] -> begin or trace error
*/
func traceBegin(ctx context.Context, pool *sql.DB) (*sql.Tx, error) {
	started := time.Now()
	if err := sqlEvent(ctx, pool, "begin", "started", started, "BEGIN", nil, nil, nil); err != nil {
		return nil, err
	}
	started = time.Now()
	tx, err := pool.BeginTx(ctx, nil)
	var connection sqlConnection = pool
	if tx != nil {
		connection = tx
	}
	err = errors.Join(err, sqlEvent(ctx, connection, "begin", "completed", started, "BEGIN", nil, nil, err))
	if err != nil && tx != nil {
		err = errors.Join(err, traceRollback(ctx, tx))
	}
	return tx, err
}

/*
traceCommit distinguishes a durable commit from a later trace-write failure.

* @brief: traceCommit -> commits and records whether the database committed
* @params: ctx [context.Context] -> caller's trace context
* @params: tx [*sql.Tx] -> actual open transaction
* @returns: committed [bool] -> true once Commit succeeds, even if tracing fails
* @returns: err [error] -> commit or trace error
*/
func traceCommit(ctx context.Context, tx *sql.Tx) (bool, error) {
	started := time.Now()
	if err := sqlEvent(ctx, tx, "commit", "started", started, "COMMIT", nil, nil, nil); err != nil {
		return false, err
	}
	started = time.Now()
	err := tx.Commit()
	committed := err == nil
	output := map[string]any{"committed": committed}
	return committed, errors.Join(err, sqlEvent(ctx, tx, "commit", "completed", started, "COMMIT", nil, output, err))
}

/*
traceRollback always attempts rollback even when its trace cannot be written.

* @brief: traceRollback -> rolls back and records transaction outcome
* @params: ctx [context.Context] -> caller's trace context
* @params: tx [*sql.Tx] -> actual transaction requiring cleanup
* @returns: err [error] -> rollback or trace error
*/
func traceRollback(ctx context.Context, tx *sql.Tx) error {
	started := time.Now()
	before := sqlEvent(ctx, tx, "rollback", "started", started, "ROLLBACK", nil, nil, nil)
	started = time.Now()
	err := tx.Rollback()
	status := "completed"
	output := map[string]any{"rolled_back": err == nil}
	if errors.Is(err, sql.ErrTxDone) {
		status = "skipped"
		output["already_finished"] = true
		err = nil
	}
	return errors.Join(before, err, sqlEvent(ctx, tx, "rollback", status, started, "ROLLBACK", nil, output, err))
}
