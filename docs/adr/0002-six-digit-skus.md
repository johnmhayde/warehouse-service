# ADR 0002: SKUs are exactly six digits

- **Status:** Accepted
- **Date:** 2026-10-21

## Context

SKUs were free-form: up to 64 characters of `A-Z`, `0-9` and `-` (e.g. `SKU-00921`). The team has agreed on a single, fixed SKU format so SKUs are easy to read out, type and scan, and so every system that handles them validates them the same way.

## Decision

1. **A SKU is exactly six ASCII digits**, matching `^[0-9]{6}$` (e.g. `123456`).
   - Leading zeros are significant: `000123` is a valid SKU, distinct from `123`.
   - Non-ASCII digits (Arabic-Indic, fullwidth, etc.), whitespace and signs are rejected.
2. **It stays a string** everywhere: the domain type (`inventory.SKU`), the `products.sku` column (`text`) and JSON. Treating it as a number would lose leading zeros.
3. **The rule is enforced in two places that must agree:** `inventory.ParseSKU` in the API, and the `products_sku_format` check constraint (migration `00006_six_digit_sku.sql`).
4. **It stays in `/api/v1`.** The JSON shape doesn't change (`sku` is still a string), so this isn't treated as a breaking change needing `httpapi/v2`. Requests with old-format SKUs now get `400 invalid SKU` instead of `404`.

## Consequences

- Migration 00006 **deletes** existing products whose SKU doesn't match, together with their `stock_adjustments` rows (the foreign key is `RESTRICT`, so they're deleted explicitly first). There is no mapping from old SKUs to new ones, and only local and test databases hold product data so far. Its Down section restores the old constraint but not the deleted rows.
- A future change to the format needs a new migration and a matching change to `inventory.ParseSKU`, and should update this ADR.
