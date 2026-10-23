# ADR 0003: Rental stock model and reservation visibility

- **Status:** Accepted
- **Date:** 2026-10-22
- **Tickets:** EP04 (WHS-401–WHS-409), WHS-305, WHS-307

## Context

The service lets users rent products for an event. Products aren't consumable: every unit that goes out comes back (or is written off). Two numbers matter for a product: how many units we own, and how many are rented out. The first schema (WHS-301) was modelled on consumable stock instead, with a `reorder_threshold` per product, and the create endpoint (WHS-303) couldn't record any stock.

Stakeholders also had to decide how much requesters see before they submit a reservation, and what admins need to see to decide on one.

## Decision

1. **`products.total_quantity` is the number of units owned.** It changes only through a `stock_adjustments` row in the same transaction, including the opening count when a product is created. A damaged or lost unit is a separate adjustment that lowers the owned count; returning a reservation never changes it.
2. **Rented-out stock is derived, not stored.** It comes from reservations that hold stock, over a date window: available for a window = owned − the most units held on any day in that window. There's no "remaining" or "rented" column to keep in sync.
3. **Only `approved` and `checked_out` reservations hold stock.** A `checked_out` reservation keeps holding stock past its end date until it's returned. `requested`, `rejected`, `cancelled` and `returned` don't hold stock.
4. **Requesters have no visibility into reservations or availability (MVP).** They submit a request for products, quantities and dates, and see only their own reservations. There's no requester-facing availability endpoint and no "N pending" count. A request for another user's reservation returns 404, so the API doesn't confirm it exists.
5. **Admins make the judgement calls.** When reviewing a request, an admin sees, for each product and the requested dates: the units available and the pending reservations that overlap. Turnaround time between rentals isn't modelled; admins allow for it.
6. **Shape of a reservation:**
   - One reservation covers several products (a header plus one line per product).
   - Dates are whole days, with no buffer between rentals.
   - A requester can edit a reservation while it's `requested`.
   - An admin can approve fewer units than requested on any line.
7. **Approval can't overbook.** Approving runs in one transaction that locks the products involved, sums the stock already held over the window, and fails with `409` if the request no longer fits. A stock adjustment that lowers `total_quantity` takes the same lock and can't go below what's already committed: the most units held on any day from today onward, counting an overdue `checked_out` reservation as held through today.
8. **`/api/v1` changes in place until it has a consumer.** Nothing is deployed and the web app isn't built, so these breaking changes stay in v1 rather than going into `httpapi/v2`:
   - `reorder_threshold` is dropped from the schema and the API.
   - The create-product request takes `total_quantity`.
   - `GET /api/v1/items/{sku}` is removed in favour of `GET /api/v1/products/{id}` (WHS-305).

   The same reasoning covers the schema: dropping `reorder_threshold` is a single migration rather than an expand/contract split ([ADR 0001](0001-database-migrations.md), decision 3), because no deployed API version reads the column.

   This exception ends when the first real consumer exists (the web app or a deployed environment). After that, the CLAUDE.md rule applies: breaking changes go in a new `httpapi/v2`.

## Consequences

- The availability calculation (WHS-403/404) serves admin routes only. Requester-facing reservation routes (WHS-405–407) return only the caller's own data.
- The admin queue and review screens (WHS-408/409) carry availability and overlapping pending reservations per line.
- The reservations schema (WHS-401) needs a header and a lines table, whole-day date ranges, and a per-line approved quantity.
- Locking products during approval serialises approvals that share a product. That's acceptable at this scale; WHS-415 tests the race for the last units.
- Local API clients (Postman requests, which aren't kept in this repo) that call `/api/v1/items/{sku}` or send `reorder_threshold` must be updated.
- If requesters later need to see availability, add it as a separate endpoint and update this ADR; nothing in the data model prevents it.
