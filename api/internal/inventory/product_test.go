package inventory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeProducts is a ProductRepository that records what it was asked to
// store and returns it as product 1.
type fakeProducts struct {
	err error

	called     bool
	gotActorID int64
	got        NewProduct

	// product is what GetProduct, UpdateProduct and SetProductActive
	// return; gotID is the id they were asked for.
	product Product
	gotID   int64

	// list is what ListProducts returns, cut to the filter's limit;
	// gotFilter is the filter it was given.
	list      []Product
	gotFilter ProductFilter

	gotUpdate ProductUpdate

	// changed is what SetProductActive returns; gotActive is the flag it
	// was asked to set.
	changed   bool
	gotActive bool
}

func (f *fakeProducts) ListProducts(ctx context.Context, filter ProductFilter) ([]Product, error) {
	f.called = true
	f.gotFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.list[:min(filter.Limit, len(f.list))], nil
}

func (f *fakeProducts) UpdateProduct(ctx context.Context, id int64, u ProductUpdate) (Product, error) {
	f.called = true
	f.gotID = id
	f.gotUpdate = u
	if f.err != nil {
		return Product{}, f.err
	}
	return f.product, nil
}

func (f *fakeProducts) CreateProduct(ctx context.Context, actorID int64, p NewProduct) (Product, error) {
	f.called = true
	f.gotActorID = actorID
	f.got = p
	if f.err != nil {
		return Product{}, f.err
	}
	return Product{ID: 1, SKU: p.SKU, Name: p.Name, Description: p.Description, Category: p.Category, TotalQuantity: p.TotalQuantity, Active: true}, nil
}

func (f *fakeProducts) GetProduct(ctx context.Context, id int64) (Product, error) {
	f.called = true
	f.gotID = id
	if f.err != nil {
		return Product{}, f.err
	}
	return f.product, nil
}

func (f *fakeProducts) SetProductActive(ctx context.Context, id int64, active bool) (Product, bool, error) {
	f.called = true
	f.gotID = id
	f.gotActive = active
	if f.err != nil {
		return Product{}, false, f.err
	}
	return f.product, f.changed, nil
}

var (
	admin     = Actor{UserID: 1, Admin: true}
	requester = Actor{UserID: 2}
)

func TestCreateProductNormalizes(t *testing.T) {
	tests := []struct {
		name string
		in   NewProduct
		want NewProduct
	}{
		{
			"minimal",
			NewProduct{SKU: "123456", Name: "Pallet jack"},
			NewProduct{SKU: "123456", Name: "Pallet jack"},
		},
		{
			"trims name and category",
			NewProduct{SKU: "123456", Name: "  Pallet jack\t", Category: " Handling ", Description: "  kept as is "},
			NewProduct{SKU: "123456", Name: "Pallet jack", Category: "Handling", Description: "  kept as is "},
		},
		{
			"blank category is uncategorized",
			NewProduct{SKU: "123456", Name: "Pallet jack", Category: "   "},
			NewProduct{SKU: "123456", Name: "Pallet jack"},
		},
		{
			"initial stock",
			NewProduct{SKU: "123456", Name: "Pallet jack", TotalQuantity: 12},
			NewProduct{SKU: "123456", Name: "Pallet jack", TotalQuantity: 12},
		},
		{
			"largest total quantity",
			NewProduct{SKU: "123456", Name: "Pallet jack", TotalQuantity: 2147483647},
			NewProduct{SKU: "123456", Name: "Pallet jack", TotalQuantity: 2147483647},
		},
		{
			"longest allowed fields",
			NewProduct{SKU: "123456", Name: strings.Repeat("é", 200), Description: strings.Repeat("é", 2000), Category: strings.Repeat("é", 100)},
			NewProduct{SKU: "123456", Name: strings.Repeat("é", 200), Description: strings.Repeat("é", 2000), Category: strings.Repeat("é", 100)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{}

			if _, err := NewProductService(repo).CreateProduct(context.Background(), admin, tt.in); err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}
			if !reflect.DeepEqual(repo.got, tt.want) {
				t.Errorf("repository got %+v, want %+v", repo.got, tt.want)
			}
		})
	}
}

func TestCreateProductValidation(t *testing.T) {
	tests := []struct {
		name    string
		in      NewProduct
		wantErr string
	}{
		{"missing name", NewProduct{SKU: "123456"}, "name is required"},
		{"blank name", NewProduct{SKU: "123456", Name: " \t\n"}, "name is required"},
		{"long name", NewProduct{SKU: "123456", Name: strings.Repeat("a", 201)}, "name must be at most 200 characters"},
		{"long description", NewProduct{SKU: "123456", Name: "x", Description: strings.Repeat("é", 2001)}, "description must be at most 2000 characters"},
		{"long category", NewProduct{SKU: "123456", Name: "x", Category: strings.Repeat("a", 101)}, "category must be at most 100 characters"},
		{"NUL in name", NewProduct{SKU: "123456", Name: "a\x00b"}, "name must not contain NUL characters"},
		{"NUL in description", NewProduct{SKU: "123456", Name: "x", Description: "a\x00"}, "description must not contain NUL characters"},
		{"NUL in category", NewProduct{SKU: "123456", Name: "x", Category: "\x00a"}, "category must not contain NUL characters"},
		{"negative total quantity", NewProduct{SKU: "123456", Name: "x", TotalQuantity: -1}, "total_quantity must be between 0 and 2147483647"},
		{"total quantity too large", NewProduct{SKU: "123456", Name: "x", TotalQuantity: 2147483648}, "total_quantity must be between 0 and 2147483647"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{}

			_, err := NewProductService(repo).CreateProduct(context.Background(), admin, tt.in)

			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("CreateProduct error = %v, want a *ValidationError", err)
			}
			if verr.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", verr.Error(), tt.wantErr)
			}
			if repo.called {
				t.Error("repository called for an invalid product")
			}
		})
	}
}

func TestCreateProductForbidden(t *testing.T) {
	tests := []struct {
		name string
		in   NewProduct
	}{
		{"valid product", NewProduct{SKU: "123456", Name: "Pallet jack"}},
		// The role is checked first, so a requester can't probe the rules.
		{"invalid product", NewProduct{SKU: "123456"}},
	}
	// The zero Actor is anonymous and must never pass the admin check.
	for _, actor := range []Actor{requester, {}} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%+v %s", actor, tt.name), func(t *testing.T) {
				repo := &fakeProducts{}

				_, err := NewProductService(repo).CreateProduct(context.Background(), actor, tt.in)

				if !errors.Is(err, ErrForbidden) {
					t.Errorf("CreateProduct error = %v, want ErrForbidden", err)
				}
				if repo.called {
					t.Error("repository called for a non-admin")
				}
			})
		}
	}
}

func TestCreateProductPassesActorToRepository(t *testing.T) {
	repo := &fakeProducts{}

	got, err := NewProductService(repo).CreateProduct(context.Background(), Actor{UserID: 7, Admin: true}, NewProduct{SKU: "123456", Name: "Pallet jack", TotalQuantity: 3})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	if repo.gotActorID != 7 {
		t.Errorf("repository got actor id %d, want 7", repo.gotActorID)
	}
	if got.TotalQuantity != 3 {
		t.Errorf("TotalQuantity = %d, want 3", got.TotalQuantity)
	}
}

func TestCreateProductRepositoryError(t *testing.T) {
	repo := &fakeProducts{err: errors.Join(errors.New("postgres: insert product"), ErrDuplicateSKU)}

	_, err := NewProductService(repo).CreateProduct(context.Background(), admin, NewProduct{SKU: "123456", Name: "Pallet jack"})

	if !errors.Is(err, ErrDuplicateSKU) {
		t.Errorf("CreateProduct error = %v, want ErrDuplicateSKU", err)
	}
}

func TestGetProduct(t *testing.T) {
	active := Product{ID: 7, SKU: "123456", Name: "Pallet jack", Active: true}
	inactive := Product{ID: 8, SKU: "654321", Name: "Old pallet jack", Active: false}
	tests := []struct {
		name    string
		actor   Actor
		product Product
		wantErr error
	}{
		{"requester, active", requester, active, nil},
		{"anonymous, active", Actor{}, active, nil},
		{"admin, active", admin, active, nil},
		{"admin, inactive", admin, inactive, nil},
		// Inactive products are hidden from everyone but admins, as if
		// they didn't exist.
		{"requester, inactive", requester, inactive, ErrHidden},
		{"anonymous, inactive", Actor{}, inactive, ErrHidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{product: tt.product}

			got, err := NewProductService(repo).GetProduct(context.Background(), tt.actor, tt.product.ID)

			if repo.gotID != tt.product.ID {
				t.Errorf("repository asked for id %d, want %d", repo.gotID, tt.product.ID)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("GetProduct error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if !reflect.DeepEqual(got, Product{}) {
					t.Errorf("GetProduct = %+v with an error, want the zero Product", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.product) {
				t.Errorf("GetProduct = %+v, want %+v", got, tt.product)
			}
		})
	}
}

func TestErrHiddenIsNotFound(t *testing.T) {
	if !errors.Is(ErrHidden, ErrNotFound) {
		t.Error("ErrHidden does not match ErrNotFound")
	}
	if errors.Is(ErrNotFound, ErrHidden) {
		t.Error("ErrNotFound matches ErrHidden")
	}
}

func TestGetProductRepositoryError(t *testing.T) {
	for _, want := range []error{ErrNotFound, errors.New("db down")} {
		repo := &fakeProducts{err: errors.Join(errors.New("postgres: get product"), want)}

		_, err := NewProductService(repo).GetProduct(context.Background(), admin, 1)

		if !errors.Is(err, want) {
			t.Errorf("GetProduct error = %v, want %v", err, want)
		}
	}
}

// products returns n active products with ids 1..n, named "Product 1"...
func products(n int) []Product {
	out := make([]Product, n)
	for i := range out {
		out[i] = Product{ID: int64(i + 1), SKU: SKU(fmt.Sprintf("%06d", i+1)), Name: fmt.Sprintf("Product %d", i+1), Active: true}
	}
	return out
}

func TestListProductsPages(t *testing.T) {
	tests := []struct {
		name      string
		stored    int
		limit     int
		wantCount int
		// wantNext is the id of the product the next page starts after, or
		// 0 on the last page.
		wantNext int64
	}{
		{"empty", 0, 50, 0, 0},
		{"fewer than the limit", 3, 50, 3, 0},
		{"exactly the limit", 3, 3, 3, 0},
		{"one more than the limit", 4, 3, 3, 3},
		{"many more than the limit", 10, 3, 3, 3},
		{"limit 1", 2, 1, 1, 1},
		{"largest limit", 101, 100, 100, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{list: products(tt.stored)}

			page, err := NewProductService(repo).ListProducts(context.Background(), requester, ProductFilter{Limit: tt.limit})
			if err != nil {
				t.Fatalf("ListProducts: %v", err)
			}

			// The repository is asked for one extra row.
			if repo.gotFilter.Limit != tt.limit+1 {
				t.Errorf("repository limit = %d, want %d", repo.gotFilter.Limit, tt.limit+1)
			}
			if len(page.Products) != tt.wantCount {
				t.Errorf("got %d products, want %d", len(page.Products), tt.wantCount)
			}
			if tt.wantNext == 0 {
				if page.Next != nil {
					t.Errorf("Next = %+v, want nil on the last page", *page.Next)
				}
				return
			}
			want := ProductCursor{Name: fmt.Sprintf("Product %d", tt.wantNext), ID: tt.wantNext}
			if page.Next == nil || *page.Next != want {
				t.Errorf("Next = %v, want %+v", page.Next, want)
			}
		})
	}
}

func TestListProductsNormalizesFilter(t *testing.T) {
	after := &ProductCursor{Name: "Pallet jack", ID: 7}
	tests := []struct {
		name string
		in   ProductFilter
		want ProductFilter
	}{
		{"defaults", ProductFilter{Limit: 50}, ProductFilter{Limit: 51}},
		{
			"trims query and category",
			ProductFilter{Query: "  pallet\t", Category: " Handling ", Limit: 10},
			ProductFilter{Query: "pallet", Category: "Handling", Limit: 11},
		},
		{
			"blank query and category are no filter",
			ProductFilter{Query: " \t", Category: "  ", Limit: 10},
			ProductFilter{Limit: 11},
		},
		{
			"keeps LIKE metacharacters for the store to escape",
			ProductFilter{Query: `50%_off\`, Limit: 10},
			ProductFilter{Query: `50%_off\`, Limit: 11},
		},
		{"passes the cursor on", ProductFilter{Limit: 10, After: after}, ProductFilter{Limit: 11, After: after}},
		{
			"longest allowed query and category",
			ProductFilter{Query: strings.Repeat("é", 200), Category: strings.Repeat("é", 100), Limit: 1},
			ProductFilter{Query: strings.Repeat("é", 200), Category: strings.Repeat("é", 100), Limit: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{}

			if _, err := NewProductService(repo).ListProducts(context.Background(), requester, tt.in); err != nil {
				t.Fatalf("ListProducts: %v", err)
			}
			if !reflect.DeepEqual(repo.gotFilter, tt.want) {
				t.Errorf("repository got %+v, want %+v", repo.gotFilter, tt.want)
			}
		})
	}
}

func TestListProductsValidation(t *testing.T) {
	tests := []struct {
		name    string
		in      ProductFilter
		wantErr string
	}{
		{"zero limit", ProductFilter{}, "limit must be between 1 and 100"},
		{"negative limit", ProductFilter{Limit: -1}, "limit must be between 1 and 100"},
		{"limit too large", ProductFilter{Limit: 101}, "limit must be between 1 and 100"},
		{"long query", ProductFilter{Query: strings.Repeat("a", 201), Limit: 10}, "q must be at most 200 characters"},
		{"NUL in query", ProductFilter{Query: "a\x00b", Limit: 10}, "q must not contain NUL characters"},
		{"long category", ProductFilter{Category: strings.Repeat("é", 101), Limit: 10}, "category must be at most 100 characters"},
		{"NUL in category", ProductFilter{Category: "\x00", Limit: 10}, "category must not contain NUL characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{}

			_, err := NewProductService(repo).ListProducts(context.Background(), admin, tt.in)

			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("ListProducts error = %v, want a *ValidationError", err)
			}
			if verr.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", verr.Error(), tt.wantErr)
			}
			if repo.called {
				t.Error("repository called for an invalid filter")
			}
		})
	}
}

func TestListProductsIncludeInactive(t *testing.T) {
	tests := []struct {
		name    string
		actor   Actor
		in      ProductFilter
		wantErr error
	}{
		{"admin", admin, ProductFilter{IncludeInactive: true, Limit: 10}, nil},
		{"requester", requester, ProductFilter{IncludeInactive: true, Limit: 10}, ErrForbidden},
		// The zero Actor is anonymous and must never pass the admin check.
		{"anonymous", Actor{}, ProductFilter{IncludeInactive: true, Limit: 10}, ErrForbidden},
		// The role is checked before the filter rules.
		{"requester with invalid filter", requester, ProductFilter{IncludeInactive: true}, ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{}

			_, err := NewProductService(repo).ListProducts(context.Background(), tt.actor, tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ListProducts error = %v, want %v", err, tt.wantErr)
			}
			if repo.called != (tt.wantErr == nil) {
				t.Errorf("repository called = %v, want %v", repo.called, tt.wantErr == nil)
			}
			if tt.wantErr == nil && !repo.gotFilter.IncludeInactive {
				t.Error("repository filter dropped IncludeInactive")
			}
		})
	}
}

// TestListProductsActiveOnlyByDefault checks that a caller of any role who
// doesn't ask for inactive products gets only active ones.
func TestListProductsActiveOnlyByDefault(t *testing.T) {
	for _, actor := range []Actor{admin, requester, {}} {
		repo := &fakeProducts{}

		if _, err := NewProductService(repo).ListProducts(context.Background(), actor, ProductFilter{Limit: 10}); err != nil {
			t.Fatalf("ListProducts as %+v: %v", actor, err)
		}
		if repo.gotFilter.IncludeInactive {
			t.Errorf("repository filter for %+v includes inactive products", actor)
		}
	}
}

func TestListProductsRepositoryError(t *testing.T) {
	want := errors.New("db down")
	repo := &fakeProducts{err: errors.Join(errors.New("postgres: select products"), want)}

	page, err := NewProductService(repo).ListProducts(context.Background(), admin, ProductFilter{Limit: 10})

	if !errors.Is(err, want) {
		t.Errorf("ListProducts error = %v, want %v", err, want)
	}
	if page.Products != nil || page.Next != nil {
		t.Errorf("ListProducts = %+v with an error, want the zero ProductPage", page)
	}
}

func ptr(s string) *string { return &s }

// formatUpdate shows the values u points to rather than the pointers.
func formatUpdate(u ProductUpdate) string {
	f := func(p *string) string {
		if p == nil {
			return "<unset>"
		}
		return fmt.Sprintf("%q", *p)
	}
	return fmt.Sprintf("{Name:%s Description:%s Category:%s}", f(u.Name), f(u.Description), f(u.Category))
}

func TestUpdateProductNormalizes(t *testing.T) {
	tests := []struct {
		name string
		in   ProductUpdate
		want ProductUpdate
	}{
		{"name only", ProductUpdate{Name: ptr("Pallet jack")}, ProductUpdate{Name: ptr("Pallet jack")}},
		{
			"trims name and category",
			ProductUpdate{Name: ptr("  Pallet jack\t"), Description: ptr("  kept as is "), Category: ptr(" Handling ")},
			ProductUpdate{Name: ptr("Pallet jack"), Description: ptr("  kept as is "), Category: ptr("Handling")},
		},
		{"empty description", ProductUpdate{Description: ptr("")}, ProductUpdate{Description: ptr("")}},
		{"empty category clears it", ProductUpdate{Category: ptr("")}, ProductUpdate{Category: ptr("")}},
		{"blank category clears it", ProductUpdate{Category: ptr(" \t ")}, ProductUpdate{Category: ptr("")}},
		{
			"longest allowed fields",
			ProductUpdate{Name: ptr(strings.Repeat("é", 200)), Description: ptr(strings.Repeat("é", 2000)), Category: ptr(strings.Repeat("é", 100))},
			ProductUpdate{Name: ptr(strings.Repeat("é", 200)), Description: ptr(strings.Repeat("é", 2000)), Category: ptr(strings.Repeat("é", 100))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{product: Product{ID: 7}}

			got, err := NewProductService(repo).UpdateProduct(context.Background(), admin, 7, tt.in)
			if err != nil {
				t.Fatalf("UpdateProduct: %v", err)
			}

			if repo.gotID != 7 {
				t.Errorf("repository got id %d, want 7", repo.gotID)
			}
			if !reflect.DeepEqual(repo.gotUpdate, tt.want) {
				t.Errorf("repository got %s, want %s", formatUpdate(repo.gotUpdate), formatUpdate(tt.want))
			}
			if got.ID != 7 {
				t.Errorf("UpdateProduct = %+v, want the repository's product", got)
			}
		})
	}
}

func TestUpdateProductDoesNotChangeCallersValues(t *testing.T) {
	name := "  Pallet jack "
	repo := &fakeProducts{}

	if _, err := NewProductService(repo).UpdateProduct(context.Background(), admin, 1, ProductUpdate{Name: &name}); err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}

	if name != "  Pallet jack " {
		t.Errorf("caller's name changed to %q", name)
	}
}

func TestUpdateProductValidation(t *testing.T) {
	tests := []struct {
		name    string
		in      ProductUpdate
		wantErr string
	}{
		{"empty name", ProductUpdate{Name: ptr("")}, "name must not be blank"},
		{"blank name", ProductUpdate{Name: ptr(" \t\n")}, "name must not be blank"},
		{"long name", ProductUpdate{Name: ptr(strings.Repeat("a", 201))}, "name must be at most 200 characters"},
		{"long description", ProductUpdate{Description: ptr(strings.Repeat("é", 2001))}, "description must be at most 2000 characters"},
		{"long category", ProductUpdate{Category: ptr(strings.Repeat("a", 101))}, "category must be at most 100 characters"},
		{"NUL in name", ProductUpdate{Name: ptr("a\x00b")}, "name must not contain NUL characters"},
		{"NUL in description", ProductUpdate{Description: ptr("a\x00")}, "description must not contain NUL characters"},
		{"NUL in category", ProductUpdate{Category: ptr("\x00a")}, "category must not contain NUL characters"},
		{"valid name, long category", ProductUpdate{Name: ptr("x"), Category: ptr(strings.Repeat("a", 101))}, "category must be at most 100 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{}

			_, err := NewProductService(repo).UpdateProduct(context.Background(), admin, 1, tt.in)

			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("UpdateProduct error = %v, want a *ValidationError", err)
			}
			if verr.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", verr.Error(), tt.wantErr)
			}
			if repo.called {
				t.Error("repository called for an invalid update")
			}
		})
	}
}

func TestUpdateProductEmpty(t *testing.T) {
	repo := &fakeProducts{}

	_, err := NewProductService(repo).UpdateProduct(context.Background(), admin, 1, ProductUpdate{})

	if !errors.Is(err, ErrEmptyUpdate) {
		t.Errorf("UpdateProduct error = %v, want ErrEmptyUpdate", err)
	}
	if repo.called {
		t.Error("repository called for an empty update")
	}
}

func TestUpdateProductForbidden(t *testing.T) {
	tests := []struct {
		name string
		in   ProductUpdate
	}{
		{"valid update", ProductUpdate{Name: ptr("Pallet jack")}},
		// The role is checked first, so a requester can't probe the rules.
		{"invalid update", ProductUpdate{Name: ptr(" ")}},
		{"empty update", ProductUpdate{}},
	}
	// The zero Actor is anonymous and must never pass the admin check.
	for _, actor := range []Actor{requester, {}} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%+v %s", actor, tt.name), func(t *testing.T) {
				repo := &fakeProducts{}

				_, err := NewProductService(repo).UpdateProduct(context.Background(), actor, 1, tt.in)

				if !errors.Is(err, ErrForbidden) {
					t.Errorf("UpdateProduct error = %v, want ErrForbidden", err)
				}
				if repo.called {
					t.Error("repository called for a non-admin")
				}
			})
		}
	}
}

func TestUpdateProductRepositoryError(t *testing.T) {
	for _, want := range []error{ErrNotFound, errors.New("db down")} {
		repo := &fakeProducts{err: errors.Join(errors.New("postgres: update product"), want)}

		_, err := NewProductService(repo).UpdateProduct(context.Background(), admin, 1, ProductUpdate{Name: ptr("x")})

		if !errors.Is(err, want) {
			t.Errorf("UpdateProduct error = %v, want %v", err, want)
		}
	}
}

func TestProductUpdateFields(t *testing.T) {
	tests := []struct {
		in   ProductUpdate
		want []string
	}{
		{ProductUpdate{}, nil},
		{ProductUpdate{Category: ptr("")}, []string{"category"}},
		{ProductUpdate{Category: ptr("x"), Name: ptr("y")}, []string{"name", "category"}},
		{ProductUpdate{Name: ptr(""), Description: ptr(""), Category: ptr("")}, []string{"name", "description", "category"}},
	}
	for _, tt := range tests {
		if got := tt.in.Fields(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s.Fields() = %v, want %v", formatUpdate(tt.in), got, tt.want)
		}
	}
}

func TestSetProductActive(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, changed := range []bool{false, true} {
			t.Run(fmt.Sprintf("active %v changed %v", active, changed), func(t *testing.T) {
				want := Product{ID: 7, SKU: "123456", Name: "Pallet jack", Active: active}
				repo := &fakeProducts{product: want, changed: changed}

				got, gotChanged, err := NewProductService(repo).SetProductActive(context.Background(), admin, 7, active)
				if err != nil {
					t.Fatalf("SetProductActive: %v", err)
				}

				if repo.gotID != 7 || repo.gotActive != active {
					t.Errorf("repository asked for id %d active %v, want 7 %v", repo.gotID, repo.gotActive, active)
				}
				if !reflect.DeepEqual(got, want) || gotChanged != changed {
					t.Errorf("SetProductActive = %+v, %v; want %+v, %v", got, gotChanged, want, changed)
				}
			})
		}
	}
}

func TestSetProductActiveForbidden(t *testing.T) {
	// The zero Actor is anonymous and must never pass the admin check.
	for _, actor := range []Actor{requester, {}} {
		for _, active := range []bool{false, true} {
			t.Run(fmt.Sprintf("%+v active %v", actor, active), func(t *testing.T) {
				repo := &fakeProducts{}

				_, _, err := NewProductService(repo).SetProductActive(context.Background(), actor, 7, active)

				if !errors.Is(err, ErrForbidden) {
					t.Errorf("SetProductActive error = %v, want ErrForbidden", err)
				}
				if repo.called {
					t.Error("repository called for a non-admin")
				}
			})
		}
	}
}

func TestSetProductActiveRepositoryError(t *testing.T) {
	for _, want := range []error{ErrNotFound, errors.New("db down")} {
		repo := &fakeProducts{err: errors.Join(errors.New("postgres: set product active"), want)}

		_, _, err := NewProductService(repo).SetProductActive(context.Background(), admin, 1, false)

		if !errors.Is(err, want) {
			t.Errorf("SetProductActive error = %v, want %v", err, want)
		}
	}
}
