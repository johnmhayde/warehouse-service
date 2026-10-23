-- +goose Up
-- Products are rented out for events and come back, so they're never
-- consumed and a reorder threshold has no meaning (docs/adr/0003). Nothing
-- is deployed yet, so the column is dropped in one step rather than split
-- expand/contract.
ALTER TABLE products DROP CONSTRAINT products_reorder_threshold_nonnegative;
ALTER TABLE products DROP COLUMN reorder_threshold;

-- +goose Down
-- Restores the 00004 column and constraint. The values dropped by Up are not
-- restored: every product comes back with no threshold (NULL).
ALTER TABLE products ADD COLUMN reorder_threshold integer;
ALTER TABLE products ADD CONSTRAINT products_reorder_threshold_nonnegative CHECK (reorder_threshold >= 0);
