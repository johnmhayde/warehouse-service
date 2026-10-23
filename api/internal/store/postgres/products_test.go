package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

// Compile-time check that Products satisfies the domain's interface.
var _ inventory.ProductRepository = (*Products)(nil)

func TestProductsCreateProduct(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	tests := []struct {
		name string
		in   inventory.NewProduct
	}{
		{"defaults", inventory.NewProduct{SKU: "000123", Name: "Pallet jack"}},
		{"all fields", inventory.NewProduct{SKU: "123456", Name: "Shrink wrap", Description: "500 mm roll", Category: "Packaging"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()

			got, err := products.CreateProduct(ctx, 1, tt.in)
			if err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			if got.ID <= 0 {
				t.Errorf("ID = %d, want a positive id", got.ID)
			}
			if got.SKU != tt.in.SKU || got.Name != tt.in.Name || got.Description != tt.in.Description || got.Category != tt.in.Category {
				t.Errorf("CreateProduct = %+v, want the fields of %+v", got, tt.in)
			}
			if got.TotalQuantity != 0 || !got.Active {
				t.Errorf("TotalQuantity, Active = %d, %v; want 0, true", got.TotalQuantity, got.Active)
			}
			if got.CreatedAt.Before(before.Add(-time.Minute)) || !got.UpdatedAt.Equal(got.CreatedAt) {
				t.Errorf("CreatedAt, UpdatedAt = %v, %v; want now, equal", got.CreatedAt, got.UpdatedAt)
			}
		})
	}
}

// stockAdjustment is a stock_adjustments row, without its id and
// created_at.
type stockAdjustment struct {
	delta, quantityAfter int
	reason               string
	actorID              int64
}

// adjustmentsOf returns productID's stock adjustments, oldest first.
func adjustmentsOf(t *testing.T, pool *pgxpool.Pool, productID int64) []stockAdjustment {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT delta, quantity_after, reason, actor_id FROM stock_adjustments WHERE product_id = $1 ORDER BY id", productID)
	if err != nil {
		t.Fatalf("query stock_adjustments: %v", err)
	}
	defer rows.Close()
	var out []stockAdjustment
	for rows.Next() {
		var a stockAdjustment
		if err := rows.Scan(&a.delta, &a.quantityAfter, &a.reason, &a.actorID); err != nil {
			t.Fatalf("scan stock_adjustments: %v", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read stock_adjustments: %v", err)
	}
	return out
}

func TestProductsCreateProductInitialStock(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	var adminID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, display_name, role) VALUES ('admin@example.com', 'Admin', 'admin') RETURNING id",
	).Scan(&adminID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	tests := []struct {
		name            string
		sku             inventory.SKU
		totalQuantity   int
		wantAdjustments []stockAdjustment
	}{
		{"no stock", "123456", 0, nil},
		{"initial stock", "654321", 12, []stockAdjustment{{delta: 12, quantityAfter: 12, reason: "Initial stock", actorID: adminID}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := products.CreateProduct(ctx, adminID, inventory.NewProduct{SKU: tt.sku, Name: "Pallet jack", TotalQuantity: tt.totalQuantity})
			if err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			if got.TotalQuantity != tt.totalQuantity {
				t.Errorf("TotalQuantity = %d, want %d", got.TotalQuantity, tt.totalQuantity)
			}
			var stored int
			if err := pool.QueryRow(ctx, "SELECT total_quantity FROM products WHERE id = $1", got.ID).Scan(&stored); err != nil {
				t.Fatalf("query total_quantity: %v", err)
			}
			if stored != tt.totalQuantity {
				t.Errorf("stored total_quantity = %d, want %d", stored, tt.totalQuantity)
			}
			if adj := adjustmentsOf(t, pool, got.ID); !reflect.DeepEqual(adj, tt.wantAdjustments) {
				t.Errorf("stock adjustments = %+v, want %+v", adj, tt.wantAdjustments)
			}
		})
	}
}

// TestProductsCreateProductInitialStockIsAtomic checks that a product isn't
// created when its initial stock adjustment can't be written.
func TestProductsCreateProductInitialStockIsAtomic(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()

	// No user has this id, so the adjustment breaks stock_adjustments_actor_fk.
	_, err := NewProducts(pool).CreateProduct(ctx, 999, inventory.NewProduct{SKU: "123456", Name: "Pallet jack", TotalQuantity: 5})
	if err == nil {
		t.Fatal("CreateProduct succeeded with an unknown actor, want an error")
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM products").Scan(&count); err != nil {
		t.Fatalf("count products: %v", err)
	}
	if count != 0 {
		t.Errorf("products = %d, want 0 (the insert rolled back)", count)
	}
}

func TestProductsCreateProductStoresUncategorizedAsNull(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()

	p, err := NewProducts(pool).CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	var isNull bool
	if err := pool.QueryRow(ctx, "SELECT category IS NULL FROM products WHERE id = $1", p.ID).Scan(&isNull); err != nil {
		t.Fatalf("query category: %v", err)
	}
	if !isNull {
		t.Error("category stored as a non-NULL value, want NULL")
	}
}

func TestProductsCreateProductDuplicateSKU(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	if _, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"}); err != nil {
		t.Fatalf("first CreateProduct: %v", err)
	}
	_, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Another"})
	if !errors.Is(err, inventory.ErrDuplicateSKU) {
		t.Errorf("CreateProduct error = %v, want ErrDuplicateSKU", err)
	}
}

func TestProductsGetProduct(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	created, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "000123", Name: "Pallet jack", Description: "Manual", Category: "Handling"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	inactive, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Shrink wrap"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE products SET active = false WHERE id = $1", inactive.ID); err != nil {
		t.Fatalf("deactivate product: %v", err)
	}
	inactive.Active = false

	for _, want := range []inventory.Product{created, inactive} {
		t.Run(string(want.SKU), func(t *testing.T) {
			got, err := products.GetProduct(ctx, want.ID)
			if err != nil {
				t.Fatalf("GetProduct: %v", err)
			}
			// The deactivation moves updated_at, so the timestamps are
			// compared on their own.
			if !got.CreatedAt.Equal(want.CreatedAt) || got.UpdatedAt.Before(want.UpdatedAt) {
				t.Errorf("CreatedAt, UpdatedAt = %v, %v; want %v and not before %v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
			}
			got.CreatedAt, got.UpdatedAt = time.Time{}, time.Time{}
			want.CreatedAt, want.UpdatedAt = time.Time{}, time.Time{}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("GetProduct = %+v, want %+v", got, want)
			}
		})
	}
}

// listFixture is a product inserted for the ListProducts tests.
type listFixture struct {
	sku, name, category string
	active              bool
}

// insertProducts inserts ps in order and returns their names by id. An
// empty category is stored as NULL.
func insertProducts(t *testing.T, pool *pgxpool.Pool, ps []listFixture) map[int64]string {
	t.Helper()
	names := make(map[int64]string)
	for _, p := range ps {
		var id int64
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO products (sku, name, category, active) VALUES ($1, $2, NULLIF($3, ''), $4) RETURNING id`,
			p.sku, p.name, p.category, p.active,
		).Scan(&id); err != nil {
			t.Fatalf("insert product %s: %v", p.sku, err)
		}
		names[id] = p.name
	}
	return names
}

// skusOf returns the SKUs of ps, in order.
func skusOf(ps []inventory.Product) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p.SKU)
	}
	return out
}

func TestProductsListProductsFilters(t *testing.T) {
	pool := pgtest.Migrated(t)
	products := NewProducts(pool)
	insertProducts(t, pool, []listFixture{
		{"000001", "Pallet jack", "Handling", true},
		{"000002", "Electric PALLET jack", "Handling", true},
		{"000003", "Old pallet jack", "Handling", false},
		{"000004", "Shrink wrap", "Packaging", true},
		{"000005", "Folding chair", "", true},
		{"000006", "50% off sign", "Signage", true},
		{"000007", "500 offcuts", "Signage", true},
		{"000008", "cable_tie", "", true},
		{"000009", "cableXtie", "", true},
		{"000010", `C:\ label`, "", true},
		{"000011", "C: label", "", true},
		{"000012", "Retired sign", "Signage", false},
	})

	// The order of these names depends on the database's collation, so this
	// test compares the SKUs as sets. TestProductsListProductsPaging covers
	// the order.
	tests := []struct {
		name     string
		filter   inventory.ProductFilter
		wantSKUs []string
	}{
		{"everything active", inventory.ProductFilter{},
			[]string{"000001", "000002", "000004", "000005", "000006", "000007", "000008", "000009", "000010", "000011"}},
		{"everything", inventory.ProductFilter{IncludeInactive: true},
			[]string{"000001", "000002", "000003", "000004", "000005", "000006", "000007", "000008", "000009", "000010", "000011", "000012"}},
		{"query ignores case", inventory.ProductFilter{Query: "pallet"}, []string{"000001", "000002"}},
		{"query matches anywhere in the name", inventory.ProductFilter{Query: "JACK"}, []string{"000001", "000002"}},
		{"query with inactive", inventory.ProductFilter{Query: "pallet", IncludeInactive: true}, []string{"000001", "000002", "000003"}},
		{"query matching nothing", inventory.ProductFilter{Query: "forklift"}, []string{}},
		// LIKE metacharacters in the query match literally.
		{"percent is literal", inventory.ProductFilter{Query: "50%"}, []string{"000006"}},
		{"percent alone", inventory.ProductFilter{Query: "%"}, []string{"000006"}},
		{"underscore is literal", inventory.ProductFilter{Query: "e_t"}, []string{"000008"}},
		{"underscore alone", inventory.ProductFilter{Query: "_"}, []string{"000008"}},
		{"backslash is literal", inventory.ProductFilter{Query: `:\`}, []string{"000010"}},
		{"category", inventory.ProductFilter{Category: "Handling"}, []string{"000001", "000002"}},
		{"category with inactive", inventory.ProductFilter{Category: "Signage", IncludeInactive: true}, []string{"000006", "000007", "000012"}},
		{"category is exact", inventory.ProductFilter{Category: "handling"}, []string{}},
		{"category is not a substring", inventory.ProductFilter{Category: "Hand"}, []string{}},
		{"query and category", inventory.ProductFilter{Query: "off", Category: "Signage"}, []string{"000006", "000007"}},
		{"query and other category", inventory.ProductFilter{Query: "pallet", Category: "Packaging"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.filter
			f.Limit = 100

			got, err := products.ListProducts(context.Background(), f)
			if err != nil {
				t.Fatalf("ListProducts: %v", err)
			}
			skus := skusOf(got)
			slices.Sort(skus)
			if !reflect.DeepEqual(skus, tt.wantSKUs) {
				t.Errorf("ListProducts SKUs = %v, want %v", skus, tt.wantSKUs)
			}
		})
	}
}

// TestProductsListProductsPaging pages through the catalog a few products
// at a time, each page starting after the last product of the one before,
// and checks the pages together are exactly one unpaged ORDER BY name, id
// query: every product once, in the database's own order. The names sort
// differently under C and natural-language collations (case, spaces,
// punctuation, accents), so the test holds whatever the collation is, and
// several are repeated so ties must be broken by id.
func TestProductsListProductsPaging(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)
	names := []string{
		"apple", "Banana", "a b", "ab", "Ab", "ab", "AB", "a-b", "a_b", "Apple",
		"banana", "Banana", "B", "b", "10", "9", "éclair", "Eclair", "eclair",
		"Zebra", "ab", "a b",
	}
	var fixtures []listFixture
	for i, name := range names {
		// Every fourth product is inactive, so both filters page through
		// gaps.
		fixtures = append(fixtures, listFixture{fmt.Sprintf("%06d", i+1), name, "", i%4 != 3})
	}
	insertProducts(t, pool, fixtures)

	for _, includeInactive := range []bool{false, true} {
		// The reference order: what Postgres returns in one query.
		where := " WHERE active"
		if includeInactive {
			where = ""
		}
		rows, err := pool.Query(ctx, "SELECT id FROM products"+where+" ORDER BY name, id")
		if err != nil {
			t.Fatalf("select reference order: %v", err)
		}
		want, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			t.Fatalf("read reference order: %v", err)
		}

		for _, limit := range []int{1, 2, 3, 5, len(want) - 1, len(want), len(want) + 1} {
			t.Run(fmt.Sprintf("include inactive %v, limit %d", includeInactive, limit), func(t *testing.T) {
				var (
					got   []int64
					after *inventory.ProductCursor
				)
				for page := 0; ; page++ {
					if page > len(want) {
						t.Fatalf("still paging after %d pages", page)
					}
					ps, err := products.ListProducts(ctx, inventory.ProductFilter{
						IncludeInactive: includeInactive, Limit: limit, After: after,
					})
					if err != nil {
						t.Fatalf("ListProducts page %d: %v", page, err)
					}
					if len(ps) > limit {
						t.Fatalf("page %d has %d products, want at most %d", page, len(ps), limit)
					}
					for _, p := range ps {
						got = append(got, p.ID)
					}
					if len(ps) < limit {
						break
					}
					last := ps[len(ps)-1]
					after = &inventory.ProductCursor{Name: last.Name, ID: last.ID}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("paged ids = %v, want the unpaged order %v", got, want)
				}
			})
		}
	}
}

// TestProductsListProductsAfter checks the page starts strictly after the
// cursor, including a cursor that names no stored product (one deleted or
// renamed since the previous page).
func TestProductsListProductsAfter(t *testing.T) {
	pool := pgtest.Migrated(t)
	products := NewProducts(pool)
	var boltIDs []int64
	for id, name := range insertProducts(t, pool, []listFixture{
		{"000001", "Anchor", "", true},
		{"000002", "Bolt", "", true},
		{"000003", "Bolt", "", true},
		{"000004", "Clamp", "", true},
	}) {
		if name == "Bolt" {
			boltIDs = append(boltIDs, id)
		}
	}
	firstBolt := min(boltIDs[0], boltIDs[1])

	tests := []struct {
		name     string
		after    inventory.ProductCursor
		wantSKUs []string
	}{
		{"after a stored product", inventory.ProductCursor{Name: "Bolt", ID: firstBolt}, []string{"000003", "000004"}},
		{"between names", inventory.ProductCursor{Name: "Banana", ID: 1}, []string{"000002", "000003", "000004"}},
		{"same name, larger id", inventory.ProductCursor{Name: "Bolt", ID: 1 << 62}, []string{"000004"}},
		{"after the last product", inventory.ProductCursor{Name: "Clamp", ID: 1 << 62}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := products.ListProducts(context.Background(), inventory.ProductFilter{Limit: 10, After: &tt.after})
			if err != nil {
				t.Fatalf("ListProducts: %v", err)
			}
			if skus := skusOf(got); !reflect.DeepEqual(skus, tt.wantSKUs) {
				t.Errorf("ListProducts SKUs = %v, want %v", skus, tt.wantSKUs)
			}
		})
	}
}

// TestProductsListProductsFields checks a listed product carries every
// field, the same as GetProduct returns.
func TestProductsListProductsFields(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	created, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "000123", Name: "Pallet jack", Description: "Manual", Category: "Handling"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	uncategorized, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Shrink wrap"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	got, err := products.ListProducts(ctx, inventory.ProductFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	want := []inventory.Product{created, uncategorized}
	if len(got) != len(want) {
		t.Fatalf("ListProducts returned %d products, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].CreatedAt.Equal(want[i].CreatedAt) || !got[i].UpdatedAt.Equal(want[i].UpdatedAt) {
			t.Errorf("product %d timestamps = %v, %v; want %v, %v", i, got[i].CreatedAt, got[i].UpdatedAt, want[i].CreatedAt, want[i].UpdatedAt)
		}
		got[i].CreatedAt, got[i].UpdatedAt = time.Time{}, time.Time{}
		want[i].CreatedAt, want[i].UpdatedAt = time.Time{}, time.Time{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListProducts = %+v, want %+v", got, want)
	}
}

func strPtr(s string) *string { return &s }

func TestProductsUpdateProduct(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	var adminID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, display_name, role) VALUES ('admin@example.com', 'Admin', 'admin') RETURNING id",
	).Scan(&adminID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	tests := []struct {
		name   string
		update inventory.ProductUpdate
		// want is the product's fields after the update, applied to
		// "Pallet jack" / "Manual" / "Handling".
		wantName, wantDescription, wantCategory string
	}{
		{"name only", inventory.ProductUpdate{Name: strPtr("Hand pallet truck")}, "Hand pallet truck", "Manual", "Handling"},
		{"description only", inventory.ProductUpdate{Description: strPtr("Hydraulic")}, "Pallet jack", "Hydraulic", "Handling"},
		{"empty description", inventory.ProductUpdate{Description: strPtr("")}, "Pallet jack", "", "Handling"},
		{"category only", inventory.ProductUpdate{Category: strPtr("Lifting")}, "Pallet jack", "Manual", "Lifting"},
		{"clear category", inventory.ProductUpdate{Category: strPtr("")}, "Pallet jack", "Manual", ""},
		{
			"every field",
			inventory.ProductUpdate{Name: strPtr("Hand pallet truck"), Description: strPtr("Hydraulic"), Category: strPtr("Lifting")},
			"Hand pallet truck", "Hydraulic", "Lifting",
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sku := inventory.SKU(fmt.Sprintf("%06d", i+1))
			created, err := products.CreateProduct(ctx, adminID, inventory.NewProduct{
				SKU: sku, Name: "Pallet jack", Description: "Manual", Category: "Handling", TotalQuantity: 12,
			})
			if err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			got, err := products.UpdateProduct(ctx, created.ID, tt.update)
			if err != nil {
				t.Fatalf("UpdateProduct: %v", err)
			}

			if got.Name != tt.wantName || got.Description != tt.wantDescription || got.Category != tt.wantCategory {
				t.Errorf("name, description, category = %q, %q, %q; want %q, %q, %q",
					got.Name, got.Description, got.Category, tt.wantName, tt.wantDescription, tt.wantCategory)
			}
			// The rest of the product is unchanged; stock only changes
			// through stock adjustments.
			if got.ID != created.ID || got.SKU != sku || got.TotalQuantity != 12 || !got.Active || !got.CreatedAt.Equal(created.CreatedAt) {
				t.Errorf("UpdateProduct = %+v, want id, SKU, total quantity, active and created_at of %+v", got, created)
			}
			// now() is the transaction's start time, so the update, in a
			// later transaction, moves updated_at past created_at.
			if !got.UpdatedAt.After(got.CreatedAt) {
				t.Errorf("UpdatedAt = %v, want after CreatedAt %v", got.UpdatedAt, got.CreatedAt)
			}

			stored, err := products.GetProduct(ctx, created.ID)
			if err != nil {
				t.Fatalf("GetProduct: %v", err)
			}
			if !reflect.DeepEqual(stored, got) {
				t.Errorf("stored product = %+v, want what UpdateProduct returned, %+v", stored, got)
			}
			var categoryIsNull bool
			if err := pool.QueryRow(ctx, "SELECT category IS NULL FROM products WHERE id = $1", created.ID).Scan(&categoryIsNull); err != nil {
				t.Fatalf("query category: %v", err)
			}
			if categoryIsNull != (tt.wantCategory == "") {
				t.Errorf("category IS NULL = %v, want %v", categoryIsNull, tt.wantCategory == "")
			}
			// Only the initial stock adjustment exists.
			if adj := adjustmentsOf(t, pool, created.ID); len(adj) != 1 {
				t.Errorf("stock adjustments = %+v, want only the initial one", adj)
			}
		})
	}
}

func TestProductsUpdateProductInactive(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	created, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE products SET active = false WHERE id = $1", created.ID); err != nil {
		t.Fatalf("deactivate product: %v", err)
	}

	got, err := products.UpdateProduct(ctx, created.ID, inventory.ProductUpdate{Name: strPtr("Old pallet jack")})
	if err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}
	if got.Name != "Old pallet jack" || got.Active {
		t.Errorf("Name, Active = %q, %v; want \"Old pallet jack\", false", got.Name, got.Active)
	}
}

func TestProductsUpdateProductNotFound(t *testing.T) {
	pool := pgtest.Migrated(t)

	_, err := NewProducts(pool).UpdateProduct(context.Background(), 999, inventory.ProductUpdate{Name: strPtr("Pallet jack")})
	if !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("UpdateProduct error = %v, want ErrNotFound", err)
	}
}

func TestProductsGetProductNotFound(t *testing.T) {
	pool := pgtest.Migrated(t)

	_, err := NewProducts(pool).GetProduct(context.Background(), 999)
	if !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("GetProduct error = %v, want ErrNotFound", err)
	}
}
