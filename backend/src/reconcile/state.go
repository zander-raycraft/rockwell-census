package reconcile

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers database/sql driver name "pgx"
)

/*
SourceRef identifies one employee in a named source system within their company.

* @brief: SourceRef -> a company-scoped external identity link
* @returns: ref [SourceRef] -> source namespace and original identifier
*/
type SourceRef struct {
	Namespace string `json:"namespace"`
	Value     string `json:"value"`
}

/*
Employee is one person on a company census. Nil fields are unknown.

* @brief: Employee -> one employee row and its optional census facts
* @returns: employee [Employee] -> id, company, name, and optional dob, ssn, status, and hire date
*/
type Employee struct {
	ID               string      `json:"id"`
	CompanyID        string      `json:"company_id"`
	FirstName        string      `json:"first_name"`
	LastName         string      `json:"last_name"`
	DOB              *string     `json:"dob,omitempty"`
	SSN              *string     `json:"ssn,omitempty"`
	EmploymentStatus *bool       `json:"employment_status,omitempty"`
	HireDate         *string     `json:"hire_date,omitempty"`
	SourceRefs       []SourceRef `json:"source_refs,omitempty"`
}

/*
Dependent is one person covered through an employee. Nil fields are unknown.

* @brief: Dependent -> one dependent row linked to an employee
* @returns: dependent [Dependent] -> id, employee, name, and optional dob, ssn, and relationship
*/
type Dependent struct {
	ID           string  `json:"id"`
	EmployeeID   string  `json:"employee_id"`
	FirstName    string  `json:"first_name"`
	LastName     string  `json:"last_name"`
	DOB          *string `json:"dob,omitempty"`
	SSN          *string `json:"ssn,omitempty"`
	Relationship *string `json:"relationship,omitempty"`
}

/*
Enrollment is one benefit row for an employee or for one of that employee's dependents.

* @brief: Enrollment -> one benefit election for an employee or dependent
* @returns: enrollment [Enrollment] -> benefit type, plan, tier, status, and effective dates
*/
type Enrollment struct {
	ID             string  `json:"id"`
	EmployeeID     string  `json:"employee_id"`
	DependentID    *string `json:"dependent_id,omitempty"`
	EnrollmentType string  `json:"enrollment_type"`
	Plan           *string `json:"plan,omitempty"`
	CoverageTier   *string `json:"coverage_tier,omitempty"`
	Status         *string `json:"status,omitempty"`
	EffectiveStart *string `json:"effective_start,omitempty"`
	EffectiveEnd   *string `json:"effective_end,omitempty"`
}

/*
OpenIssue is one census conflict. EmployeeID is nil when the person is not an employee yet.

* @brief: OpenIssue -> one open or resolved census issue
* @returns: issue [OpenIssue] -> company, optional employee, kind, detail, and status
*/
type OpenIssue struct {
	ID         string   `json:"id"`
	CompanyID  string   `json:"company_id"`
	EmployeeID *string  `json:"employee_id,omitempty"`
	Kind       string   `json:"kind"`
	Detail     string   `json:"detail"`
	Status     string   `json:"status"`
	Key        string   `json:"key,omitempty"`
	Field      string   `json:"field,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Claims     []string `json:"claims,omitempty"`
}

/*
State is the census for one company.

* @brief: State -> company identity and its employees, dependents, enrollments, and issues
* @returns: state [State] -> the four census collections for one company
*/
type State struct {
	CompanyID   string       `json:"company_id"`
	CompanyName string       `json:"company_name"`
	Employees   []Employee   `json:"employees"`
	Dependents  []Dependent  `json:"dependents"`
	Enrollments []Enrollment `json:"enrollments"`
	OpenIssues  []OpenIssue  `json:"open_issues"`
}

/*
DB is the Postgres pool and the in-process lock for each company name.

* @brief: DB -> census database pool with per-company mutexes
* @returns: db [DB] -> pool that loads and saves company state
*/
type DB struct {
	pool  *sql.DB
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

/*
Open opens a Postgres pool and pings it with the caller's context.

* @brief: Open -> opens the census database and pings Postgres
* @params: ctx [context.Context] -> cancellation and deadline for the ping
* @params: databaseURL [string] -> Postgres connection URL
* @returns: db [*DB] -> pool ready for migrations and census reads
* @returns: err [error] -> non-nil when the database could not be opened
*/
func Open(ctx context.Context, databaseURL string) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	pool, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("open: ping database: %w", err)
	}
	return &DB{pool: pool, locks: make(map[string]*sync.Mutex)}, nil
}

/*
Migrate runs each unrecorded census SQL file once, in census order, inside its own transaction.

* @brief: Migrate -> applies pending SQL files in fixed census order
* @params: ctx [context.Context] -> cancellation and deadline for the migration
* @params: dir [string] -> directory that holds the SQL migration files
* @returns: err [error] -> non-nil when a migration could not be recorded
*/
func (db *DB) Migrate(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := db.EnsureMigrations(ctx); err != nil {
		return err
	}
	for _, name := range CensusMigrations() {
		if err := db.ApplyMigration(ctx, dir, name); err != nil {
			return err
		}
	}
	return nil
}

/*
CensusMigrations returns the SQL files in the order they must be applied.

* @brief: CensusMigrations -> lists census SQL files from company through open issues
* @returns: names []string -> migration filenames, not sorted alphabetically
*/
func CensusMigrations() []string {
	return []string{
		"company.sql",
		"employees.sql",
		"dependents.sql",
		"enrollments.sql",
		"documents.sql",
		"open_issues.sql",
	}
}

/*
EnsureMigrations creates the filename ledger used to skip completed files.

* @brief: EnsureMigrations -> creates schema_migrations when it is missing
* @params: ctx [context.Context] -> cancellation and deadline for the create
* @returns: err [error] -> non-nil when the ledger table could not be created
*/
func (db *DB) EnsureMigrations(ctx context.Context) error {
	_, err := traceExec(ctx, db.pool, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY
		)
	`)
	if err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	return nil
}

/*
ApplyMigration runs one SQL file when its filename is not already recorded.

* @brief: ApplyMigration -> applies one migration file inside one transaction
* @params: ctx [context.Context] -> cancellation and deadline for this file
* @params: dir [string] -> directory that holds the SQL migration files
* @params: name [string] -> migration filename to apply or skip
* @returns: err [error] -> non-nil when the file could not be applied and recorded
*/
func (db *DB) ApplyMigration(ctx context.Context, dir, name string) (err error) {
	tx, err := traceBegin(ctx, db.pool)
	if err != nil {
		return fmt.Errorf("migrate: begin %s: %w", name, err)
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, traceRollback(ctx, tx))
		}
	}()

	var recorded int
	err = traceQueryRow(ctx, tx, `SELECT 1 FROM schema_migrations WHERE filename = $1`, []any{name}, &recorded)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("migrate: lookup %s: %w", name, err)
	}

	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("migrate: read %s: %w", name, err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return fmt.Errorf("migrate: %s contained no statements", name)
	}
	if _, err := traceExec(ctx, tx, string(body)); err != nil {
		return fmt.Errorf("migrate: apply %s: %w", name, err)
	}
	if _, err := traceExec(ctx, tx, `INSERT INTO schema_migrations (filename) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("migrate: record %s: %w", name, err)
	}
	committed, err = traceCommit(ctx, tx)
	if err != nil {
		return fmt.Errorf("migrate: commit %s: %w", name, err)
	}
	return nil
}

/*
LockCompany serializes runs for one company name in this process and does not hold a database connection.

* @brief: LockCompany -> locks one company name inside this process
* @params: name [string] -> company name whose runs must not overlap
* @returns: unlock [func()] -> releases the company so another run can start
*/
func (db *DB) LockCompany(name string) (unlock func()) {
	db.mu.Lock()
	if db.locks == nil {
		db.locks = make(map[string]*sync.Mutex)
	}
	lock := db.locks[name]
	if lock == nil {
		lock = &sync.Mutex{}
		db.locks[name] = lock
	}
	db.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

/*
Load returns the company census, creating the company when the name is new.

* @brief: Load -> reads one company census, creating the company when the name is new
* @params: ctx [context.Context] -> cancellation and deadline for the read
* @params: companyName [string] -> company name supplied by the caller
* @returns: state [State] -> employees, dependents, enrollments, and issues for that company
* @returns: err [error] -> non-nil when the census could not be read
*/
func (db *DB) Load(ctx context.Context, companyName string) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, fmt.Errorf("load: %w", err)
	}
	id, name, err := db.companyByName(ctx, companyName)
	if err != nil {
		return State{}, err
	}
	employees, err := db.loadEmployees(ctx, id)
	if err != nil {
		return State{}, err
	}
	dependents, err := db.loadDependents(ctx, id)
	if err != nil {
		return State{}, err
	}
	enrollments, err := db.loadEnrollments(ctx, id)
	if err != nil {
		return State{}, err
	}
	issues, err := db.loadOpenIssues(ctx, id)
	if err != nil {
		return State{}, err
	}
	return State{
		CompanyID:   id,
		CompanyName: name,
		Employees:   employees,
		Dependents:  dependents,
		Enrollments: enrollments,
		OpenIssues:  issues,
	}, nil
}

/*
companyByName returns the oldest company row for the name, inserting one when the name is new.

* @brief: companyByName -> finds the oldest company with this name or inserts it
* @params: ctx [context.Context] -> cancellation and deadline for the lookup
* @params: name [string] -> company name to find or create
* @returns: id [string] -> company id of the oldest matching row
* @returns: storedName [string] -> company name stored on that row
* @returns: err [error] -> non-nil when the company could not be read or inserted
*/
func (db *DB) companyByName(ctx context.Context, name string) (id string, storedName string, err error) {
	// company has no timestamp. age(xmin) is how old the inserting transaction is.
	err = traceQueryRow(ctx, db.pool, `
		SELECT id::text, name
		FROM company
		WHERE name = $1
		ORDER BY age(xmin) DESC, id
		LIMIT 1
	`, []any{name}, &id, &storedName)
	if err == nil {
		return id, storedName, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("load company: %w", err)
	}
	err = traceQueryRow(ctx, db.pool, `
		INSERT INTO company (name)
		VALUES ($1)
		RETURNING id::text, name
	`, []any{name}, &id, &storedName)
	if err != nil {
		return "", "", fmt.Errorf("load company: insert: %w", err)
	}
	return id, storedName, nil
}

/*
loadEmployees reads every employee for one company.

* @brief: loadEmployees -> reads employee rows for one company id
* @params: ctx [context.Context] -> cancellation and deadline for the query
* @params: companyID [string] -> company whose employees are read
* @returns: employees []Employee -> employee rows, empty when the company has none
* @returns: err [error] -> non-nil when the employees could not be read
*/
func (db *DB) loadEmployees(ctx context.Context, companyID string) ([]Employee, error) {
	rows, err := traceQuery(ctx, db.pool, `
		SELECT id::text,
		       company_id::text,
		       first_name,
		       last_name,
		       to_char(dob, 'YYYY-MM-DD'),
		       ssn,
		       employment_status,
		       to_char(hire_date, 'YYYY-MM-DD'), source_refs
		FROM employees
		WHERE company_id = $1::uuid
		ORDER BY id
	`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load employees: %w", err)
	}
	defer rows.Close()

	employees := []Employee{}
	for rows.Next() {
		var employee Employee
		var dob, ssn, hireDate sql.NullString
		var status sql.NullBool
		var refs []byte
		if err := rows.Scan(ctx,
			&employee.ID,
			&employee.CompanyID,
			&employee.FirstName,
			&employee.LastName,
			&dob,
			&ssn,
			&status,
			&hireDate,
			&refs,
		); err != nil {
			return nil, fmt.Errorf("load employees: %w", err)
		}
		employee.DOB = stringPtr(dob)
		employee.SSN = stringPtr(ssn)
		employee.EmploymentStatus = boolPtr(status)
		employee.HireDate = stringPtr(hireDate)
		if err := json.Unmarshal(refs, &employee.SourceRefs); err != nil {
			return nil, fmt.Errorf("load employees: source refs: %w", err)
		}
		if len(employee.SourceRefs) == 0 {
			employee.SourceRefs = nil
		}
		employees = append(employees, employee)
	}
	if err := rows.Err(ctx); err != nil {
		return nil, fmt.Errorf("load employees: %w", err)
	}
	return employees, nil
}

/*
loadDependents reads dependents of one company's employees.

* @brief: loadDependents -> reads dependent rows for one company id
* @params: ctx [context.Context] -> cancellation and deadline for the query
* @params: companyID [string] -> company whose dependents are read
* @returns: dependents []Dependent -> dependent rows, empty when the company has none
* @returns: err [error] -> non-nil when the dependents could not be read
*/
func (db *DB) loadDependents(ctx context.Context, companyID string) ([]Dependent, error) {
	rows, err := traceQuery(ctx, db.pool, `
		SELECT d.id::text,
		       d.employee_id::text,
		       d.first_name,
		       d.last_name,
		       to_char(d.dob, 'YYYY-MM-DD'),
		       d.ssn,
		       d.relationship::text
		FROM dependents AS d
		JOIN employees AS e ON e.id = d.employee_id
		WHERE e.company_id = $1::uuid
		ORDER BY d.id
	`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load dependents: %w", err)
	}
	defer rows.Close()

	dependents := []Dependent{}
	for rows.Next() {
		var dependent Dependent
		var dob, ssn, relationship sql.NullString
		if err := rows.Scan(ctx,
			&dependent.ID,
			&dependent.EmployeeID,
			&dependent.FirstName,
			&dependent.LastName,
			&dob,
			&ssn,
			&relationship,
		); err != nil {
			return nil, fmt.Errorf("load dependents: %w", err)
		}
		dependent.DOB = stringPtr(dob)
		dependent.SSN = stringPtr(ssn)
		dependent.Relationship = stringPtr(relationship)
		dependents = append(dependents, dependent)
	}
	if err := rows.Err(ctx); err != nil {
		return nil, fmt.Errorf("load dependents: %w", err)
	}
	return dependents, nil
}

/*
loadEnrollments reads benefit rows for one company's employees.

* @brief: loadEnrollments -> reads enrollment rows for one company id
* @params: ctx [context.Context] -> cancellation and deadline for the query
* @params: companyID [string] -> company whose enrollments are read
* @returns: enrollments []Enrollment -> enrollment rows, empty when the company has none
* @returns: err [error] -> non-nil when the enrollments could not be read
*/
func (db *DB) loadEnrollments(ctx context.Context, companyID string) ([]Enrollment, error) {
	rows, err := traceQuery(ctx, db.pool, `
		SELECT n.id::text,
		       n.employee_id::text,
		       n.dependent_id::text,
		       n.enrollment_type::text,
		       n.plan,
		       n.coverage_tier::text,
		       n.status::text,
		       to_char(n.effective_start, 'YYYY-MM-DD'),
		       to_char(n.effective_end, 'YYYY-MM-DD')
		FROM enrollments AS n
		JOIN employees AS e ON e.id = n.employee_id
		WHERE e.company_id = $1::uuid
		ORDER BY n.id
	`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load enrollments: %w", err)
	}
	defer rows.Close()

	enrollments := []Enrollment{}
	for rows.Next() {
		var enrollment Enrollment
		var dependentID, plan, tier, status, start, end sql.NullString
		if err := rows.Scan(ctx,
			&enrollment.ID,
			&enrollment.EmployeeID,
			&dependentID,
			&enrollment.EnrollmentType,
			&plan,
			&tier,
			&status,
			&start,
			&end,
		); err != nil {
			return nil, fmt.Errorf("load enrollments: %w", err)
		}
		enrollment.DependentID = stringPtr(dependentID)
		enrollment.Plan = stringPtr(plan)
		enrollment.CoverageTier = stringPtr(tier)
		enrollment.Status = stringPtr(status)
		enrollment.EffectiveStart = stringPtr(start)
		enrollment.EffectiveEnd = stringPtr(end)
		enrollments = append(enrollments, enrollment)
	}
	if err := rows.Err(ctx); err != nil {
		return nil, fmt.Errorf("load enrollments: %w", err)
	}
	return enrollments, nil
}

/*
loadOpenIssues reads census issues for one company.

* @brief: loadOpenIssues -> reads open issue rows for one company id
* @params: ctx [context.Context] -> cancellation and deadline for the query
* @params: companyID [string] -> company whose issues are read
* @returns: issues []OpenIssue -> issue rows, empty when the company has none
* @returns: err [error] -> non-nil when the issues could not be read
*/
func (db *DB) loadOpenIssues(ctx context.Context, companyID string) ([]OpenIssue, error) {
	rows, err := traceQuery(ctx, db.pool, `
		SELECT id::text,
		       company_id::text,
		       employee_id::text,
		       kind,
		       detail,
		       status, issue_key, field, subject, claims
		FROM open_issues
		WHERE company_id = $1::uuid
		ORDER BY id
	`, companyID)
	if err != nil {
		return nil, fmt.Errorf("load open issues: %w", err)
	}
	defer rows.Close()

	issues := []OpenIssue{}
	for rows.Next() {
		var issue OpenIssue
		var employeeID sql.NullString
		var claims []byte
		if err := rows.Scan(ctx,
			&issue.ID,
			&issue.CompanyID,
			&employeeID,
			&issue.Kind,
			&issue.Detail,
			&issue.Status,
			&issue.Key, &issue.Field, &issue.Subject, &claims,
		); err != nil {
			return nil, fmt.Errorf("load open issues: %w", err)
		}
		issue.EmployeeID = stringPtr(employeeID)
		if err := json.Unmarshal(claims, &issue.Claims); err != nil {
			return nil, fmt.Errorf("load open issues: claims: %w", err)
		}
		if len(issue.Claims) == 0 {
			issue.Claims = nil
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(ctx); err != nil {
		return nil, fmt.Errorf("load open issues: %w", err)
	}
	return issues, nil
}

/*
Save replaces one company's census rows and, when the body is non-empty, stores the source document.

* @brief: Save -> writes the next census and optional source file in one transaction
* @params: ctx [context.Context] -> cancellation and deadline for the write
* @params: next [State] -> census to store for the company
* @params: document [Document] -> source file; an empty body skips the documents row
* @returns: committed [State] -> normalized state actually committed
* @returns: err [error] -> write failure, or trace failure with committed state returned after commit
*/
func (db *DB) Save(ctx context.Context, next State, document Document) (saved State, err error) {
	if err := ctx.Err(); err != nil {
		return State{}, fmt.Errorf("save: %w", err)
	}
	ready, err := NormalizeCensus(next)
	if err != nil {
		return State{}, fmt.Errorf("save: %w", err)
	}
	tx, err := traceBegin(ctx, db.pool)
	if err != nil {
		return State{}, fmt.Errorf("save: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, traceRollback(ctx, tx))
		}
	}()

	if err := lockCompanyTx(ctx, tx, ready.CompanyID); err != nil {
		return State{}, err
	}
	if err := deleteCompanyCensus(ctx, tx, ready.CompanyID); err != nil {
		return State{}, err
	}
	if err := insertEmployees(ctx, tx, ready.CompanyID, ready.Employees); err != nil {
		return State{}, err
	}
	if err := insertDependents(ctx, tx, ready.Dependents); err != nil {
		return State{}, err
	}
	if err := insertEnrollments(ctx, tx, ready.Enrollments); err != nil {
		return State{}, err
	}
	if err := insertIssues(ctx, tx, ready.CompanyID, ready.OpenIssues); err != nil {
		return State{}, err
	}
	if len(document.Body) > 0 {
		if err := insertDocument(ctx, tx, ready.CompanyID, document); err != nil {
			return State{}, err
		}
	}
	committed, err = traceCommit(ctx, tx)
	if err != nil {
		if committed {
			return ready, fmt.Errorf("save: committed but trace failed: %w", err)
		}
		return State{}, fmt.Errorf("save: commit: %w", err)
	}
	return ready, nil
}

/*
lockCompanyTx waits for the transaction advisory lock for one company id.

* @brief: lockCompanyTx -> takes the advisory lock held until commit or rollback
* @params: ctx [context.Context] -> cancellation and deadline for the lock
* @params: tx [*sql.Tx] -> open save transaction that holds the lock
* @params: companyID [string] -> company id hashed into the advisory lock key
* @returns: err [error] -> non-nil when the lock could not be taken
*/
func lockCompanyTx(ctx context.Context, tx *sql.Tx, companyID string) error {
	rows, err := traceQuery(ctx, tx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, companyID)
	if err != nil {
		return fmt.Errorf("save: lock company: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var locked any
		if err := rows.Scan(ctx, &locked); err != nil {
			return fmt.Errorf("save: lock company: %w", err)
		}
	}
	if err := rows.Err(ctx); err != nil {
		return fmt.Errorf("save: lock company: %w", err)
	}
	return nil
}

/*
deleteCompanyCensus removes census rows for one company in foreign-key order.

* @brief: deleteCompanyCensus -> deletes enrollments, dependents, issues, then employees
* @params: ctx [context.Context] -> cancellation and deadline for the deletes
* @params: tx [*sql.Tx] -> open save transaction
* @params: companyID [string] -> company whose census rows are deleted
* @returns: err [error] -> non-nil when a delete failed
*/
func deleteCompanyCensus(ctx context.Context, tx *sql.Tx, companyID string) error {
	statements := []struct {
		table string
		query string
	}{
		{
			table: "enrollments",
			query: `DELETE FROM enrollments WHERE employee_id IN (SELECT id FROM employees WHERE company_id = $1::uuid)`,
		},
		{
			table: "dependents",
			query: `DELETE FROM dependents WHERE employee_id IN (SELECT id FROM employees WHERE company_id = $1::uuid)`,
		},
		{
			table: "open_issues",
			query: `DELETE FROM open_issues WHERE company_id = $1::uuid`,
		},
		{
			table: "employees",
			query: `DELETE FROM employees WHERE company_id = $1::uuid`,
		},
	}
	for _, statement := range statements {
		if _, err := traceExec(ctx, tx, statement.query, companyID); err != nil {
			return fmt.Errorf("save: delete %s: %w", statement.table, err)
		}
	}
	return nil
}

/*
insertEmployees writes the employee rows for one company.

* @brief: insertEmployees -> inserts employee rows in the save transaction
* @params: ctx [context.Context] -> cancellation and deadline for the inserts
* @params: tx [*sql.Tx] -> open save transaction
* @params: companyID [string] -> company id stored on every employee row
* @params: employees []Employee -> employees to insert
* @returns: err [error] -> non-nil when an employee could not be inserted
*/
func insertEmployees(ctx context.Context, tx *sql.Tx, companyID string, employees []Employee) error {
	for _, employee := range employees {
		refs, err := json.Marshal(employee.SourceRefs)
		if err != nil {
			return fmt.Errorf("save: source refs: %w", err)
		}
		if len(employee.SourceRefs) == 0 {
			refs = []byte("[]")
		}
		_, err = traceExec(ctx, tx, `
			INSERT INTO employees (
				id, company_id, first_name, last_name, dob, ssn, employment_status, hire_date, source_refs
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, $5::date, $6, $7, $8::date, $9::jsonb
			)
		`,
			employee.ID,
			companyID,
			employee.FirstName,
			employee.LastName,
			nullString(employee.DOB),
			nullString(employee.SSN),
			nullBool(employee.EmploymentStatus),
			nullString(employee.HireDate),
			string(refs),
		)
		if err != nil {
			return fmt.Errorf("save: insert employee %s: %w", employee.ID, err)
		}
	}
	return nil
}

/*
insertDependents writes dependent rows after their employees exist.

* @brief: insertDependents -> inserts dependent rows in the save transaction
* @params: ctx [context.Context] -> cancellation and deadline for the inserts
* @params: tx [*sql.Tx] -> open save transaction
* @params: dependents []Dependent -> dependents to insert
* @returns: err [error] -> non-nil when a dependent could not be inserted
*/
func insertDependents(ctx context.Context, tx *sql.Tx, dependents []Dependent) error {
	for _, dependent := range dependents {
		_, err := traceExec(ctx, tx, `
			INSERT INTO dependents (
				id, employee_id, first_name, last_name, dob, ssn, relationship
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, $5::date, $6, $7::dependent_relationship
			)
		`,
			dependent.ID,
			dependent.EmployeeID,
			dependent.FirstName,
			dependent.LastName,
			nullString(dependent.DOB),
			nullString(dependent.SSN),
			nullString(dependent.Relationship),
		)
		if err != nil {
			return fmt.Errorf("save: insert dependent %s: %w", dependent.ID, err)
		}
	}
	return nil
}

/*
insertEnrollments writes benefit rows after employees and dependents exist.

* @brief: insertEnrollments -> inserts enrollment rows in the save transaction
* @params: ctx [context.Context] -> cancellation and deadline for the inserts
* @params: tx [*sql.Tx] -> open save transaction
* @params: enrollments []Enrollment -> enrollments to insert
* @returns: err [error] -> non-nil when an enrollment could not be inserted
*/
func insertEnrollments(ctx context.Context, tx *sql.Tx, enrollments []Enrollment) error {
	for _, enrollment := range enrollments {
		_, err := traceExec(ctx, tx, `
			INSERT INTO enrollments (
				id, employee_id, dependent_id, enrollment_type, plan, coverage_tier, status,
				effective_start, effective_end
			) VALUES (
				$1::uuid, $2::uuid, $3::uuid, $4::enrollment_type, $5, $6::coverage_tier,
				$7::enrollment_status, $8::date, $9::date
			)
		`,
			enrollment.ID,
			enrollment.EmployeeID,
			nullString(enrollment.DependentID),
			enrollment.EnrollmentType,
			nullString(enrollment.Plan),
			nullString(enrollment.CoverageTier),
			nullString(enrollment.Status),
			nullString(enrollment.EffectiveStart),
			nullString(enrollment.EffectiveEnd),
		)
		if err != nil {
			return fmt.Errorf("save: insert enrollment %s: %w", enrollment.ID, err)
		}
	}
	return nil
}

/*
insertIssues writes census issues after the employees they may reference.

* @brief: insertIssues -> inserts open issue rows in the save transaction
* @params: ctx [context.Context] -> cancellation and deadline for the inserts
* @params: tx [*sql.Tx] -> open save transaction
* @params: companyID [string] -> company id stored on every issue row
* @params: issues []OpenIssue -> issues to insert
* @returns: err [error] -> non-nil when an issue could not be inserted
*/
func insertIssues(ctx context.Context, tx *sql.Tx, companyID string, issues []OpenIssue) error {
	for _, issue := range issues {
		claims, err := json.Marshal(issue.Claims)
		if err != nil {
			return fmt.Errorf("save: issue claims: %w", err)
		}
		if len(issue.Claims) == 0 {
			claims = []byte("[]")
		}
		_, err = traceExec(ctx, tx, `
			INSERT INTO open_issues (id, company_id, employee_id, kind, detail, status, issue_key, field, subject, claims)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10::jsonb)
		`,
			issue.ID,
			companyID,
			nullString(issue.EmployeeID),
			issue.Kind,
			issue.Detail,
			issue.Status,
			issue.Key, issue.Field, issue.Subject, string(claims),
		)
		if err != nil {
			return fmt.Errorf("save: insert issue %s: %w", issue.ID, err)
		}
	}
	return nil
}

/*
insertDocument stores one source file. Generated size and checksum columns are left to Postgres.

* @brief: insertDocument -> inserts one documents row with a null document type
* @params: ctx [context.Context] -> cancellation and deadline for the insert
* @params: tx [*sql.Tx] -> open save transaction
* @params: companyID [string] -> company that owns the file
* @params: document [Document] -> filename, content type, and body to store
* @returns: err [error] -> non-nil when the document could not be inserted
*/
func insertDocument(ctx context.Context, tx *sql.Tx, companyID string, document Document) error {
	body := append([]byte(nil), document.Body...)
	var id, checksum string
	var size int64
	var uploaded time.Time
	var documentType sql.NullString
	var failed bool
	err := traceQueryRow(ctx, tx, `
		INSERT INTO documents (company_id, original_filename, content_type, file_bytes)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING id::text, size_bytes, uploaded_at, checksum, document_type, failed
	`, []any{companyID, document.Filename, document.ContentType, body}, &id, &size, &uploaded, &checksum, &documentType, &failed)
	if err != nil {
		return fmt.Errorf("save: insert document: %w", err)
	}
	return nil
}

/*
NormalizeCensus copies and validates the census, retaining rejected coverage dates as issues.

* @brief: NormalizeCensus -> prepares committed rows without silently losing rejected claims
* @params: next [State] -> census the caller asked to save
* @returns: ready [State] -> rows to insert for this company
* @returns: err [error] -> non-nil when a new id could not be generated
*/
func NormalizeCensus(next State) (State, error) {
	employees, employeeIDs, err := normalizeEmployees(next.Employees)
	if err != nil {
		return State{}, err
	}
	dependents, dependentIDs, err := normalizeDependents(next.Dependents, employeeIDs)
	if err != nil {
		return State{}, err
	}
	owners := make(map[string]string, len(dependents))
	for _, dependent := range dependents {
		owners[dependent.ID] = dependent.EmployeeID
	}
	claims := cloneSlice(next.OpenIssues)
	valid := make([]Enrollment, 0, len(next.Enrollments))
	for _, enrollment := range next.Enrollments {
		employeeID, ok := employeeIDs[enrollment.EmployeeID]
		if !ok {
			return State{}, fmt.Errorf("enrollment %s: unknown employee %q", enrollment.ID, enrollment.EmployeeID)
		}
		subject := employeeID
		if enrollment.DependentID != nil && *enrollment.DependentID != "" {
			dependentID, ok := dependentIDs[*enrollment.DependentID]
			if !ok || owners[dependentID] != employeeID {
				return State{}, fmt.Errorf("enrollment %s: dependent does not belong to employee", enrollment.ID)
			}
			subject = dependentID
		}
		if !endsBeforeStart(enrollment.EffectiveStart, enrollment.EffectiveEnd) {
			valid = append(valid, enrollment)
			continue
		}
		key := strings.Join([]string{"INVALID_COVERAGE_DATES", employeeID, subject, enrollment.EnrollmentType, *enrollment.EffectiveStart, *enrollment.EffectiveEnd}, "|")
		found := false
		for i := range claims {
			if claims[i].Key == key {
				claims[i].Status = "OPEN"
				found = true
				break
			}
		}
		if !found {
			claims = append(claims, OpenIssue{
				CompanyID: next.CompanyID, EmployeeID: CloneString(&employeeID),
				Kind: "INVALID_COVERAGE_DATES", Status: "OPEN", Key: key,
				Field: "coverage_dates", Subject: subject,
				Claims: []string{*enrollment.EffectiveStart, *enrollment.EffectiveEnd},
				Detail: fmt.Sprintf("%s coverage end %s precedes start %s",
					enrollment.EnrollmentType, *enrollment.EffectiveEnd, *enrollment.EffectiveStart),
			})
		}
	}
	enrollments, err := normalizeEnrollments(valid, employeeIDs, dependentIDs)
	if err != nil {
		return State{}, err
	}
	issues, err := normalizeIssues(claims, employeeIDs)
	if err != nil {
		return State{}, err
	}
	for i := range employees {
		employees[i].CompanyID = next.CompanyID
	}
	for i := range issues {
		issues[i].CompanyID = next.CompanyID
	}
	sort.Slice(employees, func(i, j int) bool { return employees[i].ID < employees[j].ID })
	sort.Slice(dependents, func(i, j int) bool { return dependents[i].ID < dependents[j].ID })
	sort.Slice(enrollments, func(i, j int) bool { return enrollments[i].ID < enrollments[j].ID })
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	return State{CompanyID: next.CompanyID, CompanyName: next.CompanyName, Employees: employees, Dependents: dependents, Enrollments: enrollments, OpenIssues: issues}, nil
}

/*
normalizeEmployees copies employees and assigns a UUID to each empty id.

* @brief: normalizeEmployees -> copies employees and records the id stored for each original id
* @params: in []Employee -> employees from the caller
* @returns: employees []Employee -> employees with non-empty ids
* @returns: ids [map[string]string] -> original or stored id to the id that will be inserted
* @returns: err [error] -> non-nil when a UUID could not be generated
*/
func normalizeEmployees(in []Employee) ([]Employee, map[string]string, error) {
	employees := make([]Employee, 0, len(in))
	ids := make(map[string]string, len(in))
	refs := make(map[SourceRef]string)
	for _, employee := range in {
		employee.DOB = censusString(employee.DOB)
		employee.SSN = censusString(employee.SSN)
		employee.EmploymentStatus = cloneBool(employee.EmploymentStatus)
		employee.HireDate = censusString(employee.HireDate)
		employee.SourceRefs = append([]SourceRef(nil), employee.SourceRefs...)
		for _, ref := range employee.SourceRefs {
			if strings.TrimSpace(ref.Namespace) == "" || strings.TrimSpace(ref.Value) == "" {
				return nil, nil, fmt.Errorf("employee %s: source reference requires namespace and value", employee.ID)
			}
		}
		original := employee.ID
		id, err := uuidOrNew(original)
		if err != nil {
			return nil, nil, err
		}
		employee.ID = id
		if _, exists := ids[id]; exists {
			return nil, nil, fmt.Errorf("employee %s: duplicate id", id)
		}
		uniqueRefs := make([]SourceRef, 0, len(employee.SourceRefs))
		for _, ref := range employee.SourceRefs {
			owner, exists := refs[ref]
			if exists && owner != id {
				return nil, nil, fmt.Errorf("source reference %s/%s belongs to multiple employees", ref.Namespace, ref.Value)
			}
			if !exists {
				refs[ref] = id
				uniqueRefs = append(uniqueRefs, ref)
			}
		}
		employee.SourceRefs = append([]SourceRef(nil), uniqueRefs...)
		if original != "" {
			ids[original] = id
		}
		ids[id] = id
		employees = append(employees, employee)
	}
	return employees, ids, nil
}

/*
normalizeDependents copies dependents and rejects unresolved employee references.

* @brief: normalizeDependents -> keeps dependents whose employee id resolves to a stored employee
* @params: in []Dependent -> dependents from the caller
* @params: employeeIDs [map[string]string] -> employee ids that will be inserted
* @returns: dependents []Dependent -> dependents to insert
* @returns: ids [map[string]string] -> original or stored dependent id to the inserted id
* @returns: err [error] -> non-nil when a UUID could not be generated
*/
func normalizeDependents(in []Dependent, employeeIDs map[string]string) ([]Dependent, map[string]string, error) {
	dependents := make([]Dependent, 0, len(in))
	ids := make(map[string]string, len(in))
	for _, dependent := range in {
		dependent.DOB = censusString(dependent.DOB)
		dependent.SSN = censusString(dependent.SSN)
		dependent.Relationship = censusString(dependent.Relationship)
		if mapped, ok := employeeIDs[dependent.EmployeeID]; ok {
			dependent.EmployeeID = mapped
		}
		if _, ok := employeeIDs[dependent.EmployeeID]; !ok {
			return nil, nil, fmt.Errorf("dependent %s: unknown employee %q", dependent.ID, dependent.EmployeeID)
		}
		original := dependent.ID
		id, err := uuidOrNew(original)
		if err != nil {
			return nil, nil, err
		}
		dependent.ID = id
		if original != "" {
			ids[original] = id
		}
		ids[id] = id
		dependents = append(dependents, dependent)
	}
	return dependents, ids, nil
}

/*
normalizeEnrollments copies enrollments and rejects invalid references or date ranges.

* @brief: normalizeEnrollments -> rewrites ids and validates enrollment references and dates
* @params: in []Enrollment -> enrollments from the caller
* @params: employeeIDs [map[string]string] -> employee ids that will be inserted
* @params: dependentIDs [map[string]string] -> dependent ids that will be inserted
* @returns: enrollments []Enrollment -> enrollments to insert
* @returns: err [error] -> non-nil when a UUID could not be generated
*/
func normalizeEnrollments(in []Enrollment, employeeIDs, dependentIDs map[string]string) ([]Enrollment, error) {
	enrollments := make([]Enrollment, 0, len(in))
	for _, enrollment := range in {
		enrollment.DependentID = CloneString(enrollment.DependentID)
		enrollment.Plan = censusString(enrollment.Plan)
		enrollment.CoverageTier = censusString(enrollment.CoverageTier)
		enrollment.Status = censusString(enrollment.Status)
		enrollment.EffectiveStart = censusString(enrollment.EffectiveStart)
		enrollment.EffectiveEnd = censusString(enrollment.EffectiveEnd)
		if mapped, ok := employeeIDs[enrollment.EmployeeID]; ok {
			enrollment.EmployeeID = mapped
		}
		if _, ok := employeeIDs[enrollment.EmployeeID]; !ok {
			return nil, fmt.Errorf("enrollment %s: unknown employee %q", enrollment.ID, enrollment.EmployeeID)
		}
		if enrollment.DependentID != nil && *enrollment.DependentID != "" {
			mapped, ok := dependentIDs[*enrollment.DependentID]
			if !ok {
				return nil, fmt.Errorf("enrollment %s: unknown dependent %q", enrollment.ID, *enrollment.DependentID)
			}
			enrollment.DependentID = CloneString(&mapped)
		} else {
			enrollment.DependentID = nil
		}
		if endsBeforeStart(enrollment.EffectiveStart, enrollment.EffectiveEnd) {
			return nil, fmt.Errorf("enrollment %s: effective end precedes start", enrollment.ID)
		}
		id, err := uuidOrNew(enrollment.ID)
		if err != nil {
			return nil, err
		}
		enrollment.ID = id
		enrollments = append(enrollments, enrollment)
	}
	return enrollments, nil
}

/*
normalizeIssues copies issues, rewrites a known employee id, and stores a blank employee id as nil.

* @brief: normalizeIssues -> fills empty issue ids and clears a blank employee id
* @params: in []OpenIssue -> issues from the caller
* @params: employeeIDs [map[string]string] -> employee ids that will be inserted
* @returns: issues []OpenIssue -> issues to insert
* @returns: err [error] -> non-nil when a UUID could not be generated
*/
func normalizeIssues(in []OpenIssue, employeeIDs map[string]string) ([]OpenIssue, error) {
	issues := make([]OpenIssue, 0, len(in))
	for _, issue := range in {
		issue.EmployeeID = CloneString(issue.EmployeeID)
		issue.Claims = append([]string(nil), issue.Claims...)
		if issue.EmployeeID != nil && *issue.EmployeeID != "" {
			mapped, ok := employeeIDs[*issue.EmployeeID]
			if !ok {
				return nil, fmt.Errorf("issue %s: unknown employee %q", issue.ID, *issue.EmployeeID)
			}
			issue.EmployeeID = CloneString(&mapped)
		} else {
			issue.EmployeeID = nil
		}
		id, err := uuidOrNew(issue.ID)
		if err != nil {
			return nil, err
		}
		issue.ID = id
		issues = append(issues, issue)
	}
	return issues, nil
}

/*
uuidOrNew keeps a non-empty id and generates a UUID when the id is empty.

* @brief: uuidOrNew -> returns the given id or a new UUID
* @params: id [string] -> id supplied by the caller
* @returns: stored [string] -> id that will be inserted
* @returns: err [error] -> non-nil when a UUID could not be generated
*/
func uuidOrNew(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	return CensusUUID()
}

/*
CensusUUID returns a random version-4 UUID string.

* @brief: CensusUUID -> generates one version-4 UUID
* @returns: id [string] -> lowercase hyphenated UUID
* @returns: err [error] -> non-nil when random bytes could not be read
*/
func CensusUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

/*
endsBeforeStart reports whether both dates are present and the end is before the start.

* @brief: endsBeforeStart -> compares an enrollment's effective dates
* @params: start [*string] -> effective start, YYYY-MM-DD, nil, or empty
* @params: end [*string] -> effective end, YYYY-MM-DD, nil, or empty
* @returns: before [bool] -> true when end is a real date before start
*/
func endsBeforeStart(start, end *string) bool {
	if start == nil || end == nil || *start == "" || *end == "" {
		return false
	}
	startDate, startErr := time.Parse(time.DateOnly, *start)
	endDate, endErr := time.Parse(time.DateOnly, *end)
	if startErr != nil || endErr != nil {
		return false
	}
	return endDate.Before(startDate)
}

/*
nullString returns nil for a nil or empty string so Postgres stores NULL.

* @brief: nullString -> converts an optional string into a database value
* @params: value [*string] -> optional text from the census
* @returns: stored [any] -> the string, or nil when it should be SQL NULL
*/
func nullString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

/*
nullBool returns nil for a nil bool so Postgres stores NULL.

* @brief: nullBool -> converts an optional bool into a database value
* @params: value [*bool] -> optional employment status
* @returns: stored [any] -> the bool, or nil when it should be SQL NULL
*/
func nullBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

/*
CloneString copies a string so the caller cannot share the pointer with stored rows.

* @brief: CloneString -> copies an optional string
* @params: value [*string] -> string to copy
* @returns: copied [*string] -> independent pointer, or nil when value is nil
*/
func CloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

/*
cloneBool copies a bool so the caller cannot share the pointer with stored rows.

* @brief: cloneBool -> copies an optional bool
* @params: value [*bool] -> bool to copy
* @returns: copied [*bool] -> independent pointer, or nil when value is nil
*/
func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

/*
stringPtr converts a nullable text column into an optional string.

* @brief: stringPtr -> copies a nullable SQL string into a Go pointer
* @params: value [sql.NullString] -> scanned text column
* @returns: copied [*string] -> nil for NULL or an empty string
*/
func stringPtr(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	return CloneString(&value.String)
}

/*
boolPtr converts a nullable boolean column into an optional bool.

* @brief: boolPtr -> copies a nullable SQL bool into a Go pointer
* @params: value [sql.NullBool] -> scanned boolean column
* @returns: copied [*bool] -> nil for NULL, otherwise a pointer to the scanned bool
*/
func boolPtr(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	return cloneBool(&value.Bool)
}

/*
cloneSlice copies a slice so the caller cannot append over the scanned backing array.

* @brief: cloneSlice -> returns a new slice with the same elements
* @params: in []T -> slice to copy
* @returns: out []T -> copy with its own backing array, empty rather than nil
*/
func cloneSlice[T any](in []T) []T {
	out := make([]T, len(in))
	copy(out, in)
	return out
}

/*
Close closes the Postgres pool.

* @brief: Close -> closes the census database pool
* @returns: err [error] -> non-nil when the pool could not be closed
*/
func (db *DB) Close() error {
	if db.pool == nil {
		return nil
	}
	if err := db.pool.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

/*
censusString copies a nullable census field using the same empty-value rule as SQL storage.

* @brief: censusString -> copies known text and preserves empty text as unknown
* @params: value [*string] -> optional census text
* @returns: copied [*string] -> independent nonempty value or nil
*/
func censusString(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	return CloneString(value)
}

/*
Summary counts saved members and outstanding review evidence without storing another census table.

* @brief: Summary -> current member and issue totals
* @returns: summary [Summary] -> counts, with held identities deduplicated by literal identity evidence
*/
type Summary struct {
	Employees                    int `json:"employees"`
	Dependents                   int `json:"dependents"`
	HeldIdentities               int `json:"held_identities"`
	MembersMissingRequiredFields int `json:"members_missing_required_fields"`
	OpenIssues                   int `json:"open_issues"`
	ResolvedIssues               int `json:"resolved_issues"`
}

/*
Summarize computes counts; identical held name, role, subscriber, DOB and SSN claims count once across feeds.

* @brief: Summarize -> counts members and review claims from existing state
* @params: state [State] -> current company census
* @returns: summary [Summary] -> totals; held identities are evidence groups, not confirmed additional people
*/
func Summarize(state State) Summary {
	summary := Summary{Employees: len(state.Employees), Dependents: len(state.Dependents)}
	for _, employee := range state.Employees {
		if employee.DOB == nil || employee.SSN == nil {
			summary.MembersMissingRequiredFields++
		}
	}
	for _, dependent := range state.Dependents {
		if dependent.Relationship == nil || strings.TrimSpace(*dependent.Relationship) == "" {
			summary.MembersMissingRequiredFields++
		}
	}
	held := map[string]bool{}
	for _, issue := range state.OpenIssues {
		if issue.Status == "RESOLVED" {
			summary.ResolvedIssues++
			continue
		}
		summary.OpenIssues++
		if issue.Field != "identity" && issue.Kind != "UNMAPPED_SOURCE_FIELD" {
			continue
		}
		for _, claim := range issue.Claims {
			var person Proposed
			if json.Unmarshal([]byte(claim), &person) != nil || (person.FirstName == "" && person.LastName == "") {
				continue
			}
			identity := []string{person.Role, foldPerson(person.FirstName, person.LastName), normKey(person.Subscriber), StringValue(person.DOB), digitsOnly(StringValue(person.SSN))}
			key, _ := json.Marshal(identity)
			held[string(key)] = true
		}
	}
	summary.HeldIdentities = len(held)
	return summary
}

/*
Company identifies one saved company for the frontend selector.

* @brief: Company -> saved company identity and name
* @returns: value [Company] -> selectable company
*/
type Company struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

/*
Companies lists saved companies without creating a census or loading member data.

* @brief: Companies -> reads existing company names
* @params: ctx [context.Context] -> request cancellation
* @returns: companies [[]Company] -> companies ordered by name
* @returns: err [error] -> database failure
*/
func (db *DB) Companies(ctx context.Context) ([]Company, error) {
	rows, err := traceQuery(ctx, db.pool, `SELECT id::text, name FROM company ORDER BY lower(name), id`)
	if err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	defer rows.Close()
	companies := []Company{}
	for rows.Next() {
		var company Company
		if err := rows.Scan(ctx, &company.ID, &company.Name); err != nil {
			return nil, fmt.Errorf("list companies: %w", err)
		}
		companies = append(companies, company)
	}
	if err := rows.Err(ctx); err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	return companies, nil
}
