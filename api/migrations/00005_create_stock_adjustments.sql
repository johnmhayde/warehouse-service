-- +goose Up
-- One row per change to a product's stock, kept as the audit history.
-- Rows are only ever inserted.
CREATE TABLE stock_adjustments (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    product_id     bigint NOT NULL,
    -- Signed change to products.total_quantity.
    delta          integer NOT NULL,
    -- products.total_quantity after this adjustment was applied.
    quantity_after integer NOT NULL,
    reason         text NOT NULL,
    -- The user who made the change.
    actor_id       bigint NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),

    -- RESTRICT: products and users with history can't be deleted; mark them
    -- inactive instead.
    CONSTRAINT stock_adjustments_product_fk FOREIGN KEY (product_id)
        REFERENCES products (id) ON DELETE RESTRICT,
    CONSTRAINT stock_adjustments_actor_fk FOREIGN KEY (actor_id)
        REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT stock_adjustments_delta_nonzero CHECK (delta <> 0),
    CONSTRAINT stock_adjustments_quantity_after_nonnegative CHECK (quantity_after >= 0),
    CONSTRAINT stock_adjustments_reason_not_blank CHECK (btrim(reason) <> '')
);

-- A product's history, newest first.
CREATE INDEX stock_adjustments_product_created_idx ON stock_adjustments (product_id, created_at DESC);
-- Postgres doesn't index foreign keys automatically; this one serves "what
-- did this user change" and the RESTRICT check when a user is deleted.
CREATE INDEX stock_adjustments_actor_idx ON stock_adjustments (actor_id);

-- +goose Down
DROP TABLE stock_adjustments;
