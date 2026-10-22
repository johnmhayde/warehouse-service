-- +goose Up
-- SKUs are now exactly six ASCII digits (docs/adr/0002-six-digit-skus.md).
--
-- Existing products whose SKU doesn't fit are deleted, not converted: there
-- is no mapping from the old free-form SKUs to the new numbers, and only
-- local and test databases hold product data so far.
--
-- stock_adjustments_product_fk is ON DELETE RESTRICT so that history can't
-- vanish as a side effect of deleting a product. It stays that way; instead
-- the adjustments of the products being removed are deleted explicitly
-- first. This is a deliberate one-off: the API itself never deletes
-- history. Users are not touched.
--
-- Everything below runs in goose's per-migration transaction, so either all
-- of it applies or none does. No statement needs StatementBegin/End.
DELETE FROM stock_adjustments
WHERE product_id IN (SELECT id FROM products WHERE sku !~ '^[0-9]{6}$');

DELETE FROM products WHERE sku !~ '^[0-9]{6}$';

ALTER TABLE products DROP CONSTRAINT products_sku_format;
-- Must match inventory.ParseSKU in the API.
ALTER TABLE products ADD CONSTRAINT products_sku_format CHECK (sku ~ '^[0-9]{6}$');

-- +goose Down
-- Restores the previous format. Every six-digit SKU also matches it, so this
-- can't fail on existing rows. The rows deleted by Up are not restored.
ALTER TABLE products DROP CONSTRAINT products_sku_format;
-- Restores the 00004 constraint. The API is not rolled back with it, so
-- inventory.ParseSKU stays stricter than this.
ALTER TABLE products ADD CONSTRAINT products_sku_format CHECK (sku ~ '^[A-Z0-9-]{1,64}$');
