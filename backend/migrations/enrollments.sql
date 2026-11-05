CREATE TYPE enrollment_type AS ENUM ('MEDICAL', 'DENTAL', 'VISION');


/*
    ENUM:
        EMPLOYEE -> Employee only
        EMPLOYEE_SPOUSE_ONLY -> Employee and spouse
        EMPLOYEE_CHILDREN -> Employee and children
        EMPLOYEE_FAMILY -> Employee and spouse and children
*/
CREATE TYPE coverage_tier AS ENUM (
    'EMPLOYEE',
    'EMPLOYEE_SPOUSE_ONLY',
    'EMPLOYEE_CHILDREN',
    'EMPLOYEE_FAMILY'
);

CREATE TYPE enrollment_status AS ENUM ('ENROLLED', 'WAIVED', 'PENDING', 'TERMINATED');

-- One row per person per benefit; a null dependent_id identifies the employee.
CREATE TABLE enrollments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id     UUID NOT NULL REFERENCES employees (id),
    dependent_id    UUID,
    enrollment_type enrollment_type NOT NULL,
    plan            TEXT,
    coverage_tier   coverage_tier,
    status          enrollment_status,
    effective_start DATE,
    effective_end   DATE,

    -- A dependent can only enroll under their own employee.
    FOREIGN KEY (employee_id, dependent_id) REFERENCES dependents (employee_id, id),
    CHECK (effective_end >= effective_start)
);

CREATE INDEX enrollments_employee_dependent_idx ON enrollments (employee_id, dependent_id);
