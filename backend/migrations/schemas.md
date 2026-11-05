TABLES:


COMPANY
    ID -> PK UUID
    NAME -> String

DOCUMENTS
    ID -> PK UUID
    COMPANY_ID -> UUID FK TABLE:COMPANY.ID
    ORIGINAL FILENAME -> String
    CONTENT TYPE -> String "MIME type, e.g. text/csv or application/pdf"
    FILE BYTES -> BYTEA "Original uploaded file contents"
    SIZE BYTES -> BigInt GENERATED FROM FILE BYTES
    UPLOADED AT -> TIMESTAMPTZ DEFAULT NOW()
    CHECKSUM -> String GENERATED FROM FILE BYTES "SHA-256"
    DOCUMENT TYPE -> String OPTIONAL "Payroll, enrollment, census, etc.; unknown until detected"
    FAILED -> Boolean DEFAULT FALSE "TRUE after automatic processing exhausts its retries; needs manual review"

EMPLOYEES
    ID -> PK UUID
    COMPANY_ID -> UUID FK TABLE:COMPANY.ID
    FIRST NAME -> String
    LAST NAME -> String
    DOB -> Date OPTIONAL
    SSN -> String OPTIONAL
    EMPLOYMENT STATUS -> Boolean OPTIONAL "TRUE = active, FALSE = inactive, NULL = unknown"
    HIRE DATE -> Date OPTIONAL

DEPENDENTS
    ID -> PK UUID
    EMPLOYEE_ID -> UUID FK TABLE:EMPLOYEES.ID
    FIRST NAME -> String
    LAST NAME -> String
    DOB -> Date OPTIONAL
    SSN -> String OPTIONAL
    RELATIONSHIP -> ENUM { SPOUSE, CHILD, OTHER } OPTIONAL "Missing or unclear stays unknown"

ENROLLMENTS
    ID -> PK UUID
    EMPLOYEE_ID -> UUID FK TABLE:EMPLOYEES.ID "Employee whose benefits this falls under"
    DEPENDENT_ID -> UUID FK TABLE:DEPENDENTS.ID OPTIONAL "Empty means this record is for the employee"
    ENROLLMENT TYPE -> ENUM { MEDICAL, DENTAL, VISION }
    PLAN -> String OPTIONAL "Specific insurance option; may be missing or waived"
    COVERAGE TIER -> ENUM { EMPLOYEE, EMPLOYEE_SPOUSE_ONLY, EMPLOYEE_CHILDREN, EMPLOYEE_FAMILY } OPTIONAL
    STATUS -> ENUM { ENROLLED, WAIVED, PENDING, TERMINATED } OPTIONAL
    EFFECTIVE START -> Date OPTIONAL
    EFFECTIVE END -> Date OPTIONAL

NOTES:
    ONE ENROLLMENT ROW -> One person's record for one benefit
    DEPENDENT_ID -> When present, the dependent must belong to the referenced EMPLOYEE_ID
    COVERAGE TIER -> Per benefit election, not per employee; may repeat across covered household members
    UNKNOWN VALUES -> Leave missing values empty; do not guess a tier, status, or relationship
    NO ENROLLMENT RECORD -> No recorded coverage information; does not automatically mean WAIVED
    DEPENDENTS ENROLLED -> Retrieve from enrollment records for the benefit, status, and date being checked
    RELATIONSHIP LISTS -> Retrieve through foreign keys; no duplicate UUID[] lists on parent tables
    SOURCE EMPLOYEE IDS -> Preserve with their source system separately from the internal employee UUID
    ONE DOCUMENT ROW -> One uploaded file belonging to one company
    DOCUMENT CONTENTS -> Preserve original bytes; extracted records live in their respective tables
    DOCUMENT CHECKSUM -> Identifies identical contents; duplicate uploads remain allowed
    DOCUMENT FAILED -> FALSE means no terminal processing failure; it does not prove processing completed
    PROCESSING QUEUE -> Tracks document ID and attempt count; retry once after the first failure
    RETRIES EXHAUSTED -> Worker sets FAILED to TRUE after the second failed attempt and stops automatic retries
    PROCESSING SUCCESS -> Commit extracted results before acknowledging the queue job; clear FAILED after a successful reprocess

INGESTION / RECONCILIATION (NEXT):
    FIELD SOURCES -> Track original values and where they came from
    CONFLICTS / REVIEW -> Preserve conflicting values and recorded resolution decisions

