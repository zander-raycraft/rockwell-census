CREATE TABLE employees (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL REFERENCES company (id),
    first_name        TEXT NOT NULL,
    last_name         TEXT NOT NULL,
    dob               DATE,
    ssn               TEXT,
    employment_status BOOLEAN,
    hire_date         DATE,
    source_refs       JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX employees_company_id_idx ON employees (company_id);
