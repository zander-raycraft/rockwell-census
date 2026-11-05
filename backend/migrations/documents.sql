CREATE TABLE documents (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL REFERENCES company (id),
    original_filename TEXT NOT NULL,
    content_type      TEXT NOT NULL,
    file_bytes        BYTEA NOT NULL,
    size_bytes        BIGINT GENERATED ALWAYS AS (octet_length(file_bytes)) STORED,
    uploaded_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    checksum          TEXT GENERATED ALWAYS AS (encode(sha256(file_bytes), 'hex')) STORED,
    document_type     TEXT,
    -- Set after retries are exhausted; FALSE does not indicate completion.
    failed            BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX documents_company_id_idx ON documents (company_id);
