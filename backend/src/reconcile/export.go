package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

/*
NewDB wraps a database pool for tests that open their own connection.

* @brief: NewDB -> stores a pool on a census database handle
* @params: pool [*sql.DB] -> open database pool
* @returns: db [*DB] -> handle that uses the supplied pool
*/
func NewDB(pool *sql.DB) *DB {
	return &DB{pool: pool, locks: make(map[string]*sync.Mutex)}
}

/*
QueryRow scans one SQL row.

* @brief: QueryRow -> runs one query and scans the first row
* @params: ctx [context.Context] -> cancellation and deadline
* @params: query [string] -> SQL text
* @params: dest [any] -> scan destinations
* @returns: err [error] -> non-nil when the query or scan fails
*/
func (db *DB) QueryRow(ctx context.Context, query string, dest ...any) error {
	if db == nil || db.pool == nil {
		return fmt.Errorf("query: database is nil")
	}
	return traceQueryRow(ctx, db.pool, query, nil, dest...)
}

/*
Exec runs one SQL statement.

* @brief: Exec -> runs one statement on the census pool
* @params: ctx [context.Context] -> cancellation and deadline
* @params: query [string] -> SQL text
* @params: args [any] -> query arguments
* @returns: err [error] -> non-nil when the statement fails
*/
func (db *DB) Exec(ctx context.Context, query string, args ...any) error {
	if db == nil || db.pool == nil {
		return fmt.Errorf("exec: database is nil")
	}
	_, err := traceExec(ctx, db.pool, query, args...)
	return err
}

/*
RemoveCompany deletes one company and its census rows.

* @brief: RemoveCompany -> deletes documents, census rows, and the company
* @params: ctx [context.Context] -> cancellation and deadline
* @params: companyID [string] -> company to delete
* @returns: err [error] -> non-nil when the delete does not commit
*/
func (db *DB) RemoveCompany(ctx context.Context, companyID string) (err error) {
	if db == nil || db.pool == nil {
		return fmt.Errorf("delete company: database is nil")
	}
	tx, err := traceBegin(ctx, db.pool)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, traceRollback(ctx, tx))
		}
	}()
	if _, err := traceExec(ctx, tx, "DELETE FROM documents WHERE company_id=$1::uuid", companyID); err != nil {
		return err
	}
	if err := deleteCompanyCensus(ctx, tx, companyID); err != nil {
		return err
	}
	if _, err := traceExec(ctx, tx, "DELETE FROM company WHERE id=$1::uuid", companyID); err != nil {
		return err
	}
	committed, err = traceCommit(ctx, tx)
	return err
}
