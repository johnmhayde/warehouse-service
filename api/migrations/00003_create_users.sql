-- +goose Up
CREATE TABLE users (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- The identity provider's subject ("sub" claim). NULL until real
    -- authentication lands (EP07); unique once set.
    external_subject text,
    email            text NOT NULL,
    display_name     text NOT NULL,
    role             text NOT NULL DEFAULT 'requester',
    active           boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT users_external_subject_key UNIQUE (external_subject),
    CONSTRAINT users_external_subject_not_blank CHECK (btrim(external_subject) <> ''),
    CONSTRAINT users_email_format CHECK (email ~ '^[^@[:space:]]+@[^@[:space:]]+$'),
    CONSTRAINT users_display_name_not_blank CHECK (btrim(display_name) <> ''),
    -- A check constraint rather than an enum type: adding a role later is a
    -- plain constraint swap instead of ALTER TYPE.
    CONSTRAINT users_role_check CHECK (role IN ('requester', 'admin'))
);

-- Emails are unique regardless of case.
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE users;
