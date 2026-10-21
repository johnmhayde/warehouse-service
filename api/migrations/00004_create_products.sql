-- +goose Up
CREATE TABLE products (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku               text NOT NULL,
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    -- NULL means uncategorized.
    category          text,
    -- Current stock on hand across the warehouse. Changed only together with
    -- a stock_adjustments row, in the same transaction.
    total_quantity    integer NOT NULL DEFAULT 0,
    -- Below this, the product counts as low stock. NULL means no threshold.
    reorder_threshold integer,
    active            boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT products_sku_key UNIQUE (sku),
    -- Must match inventory.ParseSKU in the API.
    CONSTRAINT products_sku_format CHECK (sku ~ '^[A-Z0-9-]{1,64}$'),
    CONSTRAINT products_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT products_category_not_blank CHECK (btrim(category) <> ''),
    CONSTRAINT products_total_quantity_nonnegative CHECK (total_quantity >= 0),
    CONSTRAINT products_reorder_threshold_nonnegative CHECK (reorder_threshold >= 0)
);

-- Lookup by SKU uses the products_sku_key unique index.
CREATE INDEX products_category_idx ON products (category);
CREATE INDEX products_active_idx ON products (active);
-- Trigram index so name ILIKE '%term%' doesn't scan the table (needs pg_trgm,
-- migration 00001).
CREATE INDEX products_name_trgm_idx ON products USING gin (name gin_trgm_ops);

CREATE TRIGGER products_set_updated_at BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE products;
