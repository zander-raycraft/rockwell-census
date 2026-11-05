CREATE TYPE dependent_relationship AS ENUM ('SPOUSE', 'CHILD', 'OTHER');

CREATE TABLE dependents (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id  UUID NOT NULL REFERENCES employees (id),
    first_name   TEXT NOT NULL,
    last_name    TEXT NOT NULL,
    dob          DATE,
    ssn          TEXT,
    relationship dependent_relationship,

    -- Also supports retrieving an employee's dependents.
    UNIQUE (employee_id, id)
);
