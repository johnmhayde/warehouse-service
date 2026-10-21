-- +goose Up
-- pg_trgm provides trigram indexes, which make ILIKE '%term%' searches
-- (product name search) fast. It ships with Postgres and is a trusted
-- extension, so the database owner can create it without superuser.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
DROP EXTENSION IF EXISTS pg_trgm;
