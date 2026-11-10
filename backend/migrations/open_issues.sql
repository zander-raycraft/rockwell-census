CREATE TABLE open_issues (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES company (id),
    employee_id UUID,
    kind        TEXT NOT NULL,
    detail      TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('OPEN', 'RESOLVED')),
    issue_key   TEXT NOT NULL DEFAULT '',
    field       TEXT NOT NULL DEFAULT '',
    subject     TEXT NOT NULL DEFAULT '',
    claims      JSONB NOT NULL DEFAULT '[]'::jsonb,
    FOREIGN KEY (employee_id) REFERENCES employees (id)
);

CREATE INDEX open_issues_company_id_idx ON open_issues (company_id);
