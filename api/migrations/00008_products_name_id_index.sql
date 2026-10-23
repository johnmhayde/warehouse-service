-- +goose Up
-- The product list (GET /api/v1/products) is ordered by (name, id) and
-- pages by keyset, WHERE (name, id) > ($1, $2). This index serves both the
-- order and the comparison, so a page reads only its own rows. Nothing is
-- deployed yet and the table is small, so the index is built in the
-- migration's transaction rather than CONCURRENTLY.
CREATE INDEX products_name_id_idx ON products (name, id);

-- +goose Down
DROP INDEX products_name_id_idx;
