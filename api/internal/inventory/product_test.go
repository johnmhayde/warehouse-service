package inventory

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeProducts is a ProductRepository that records what it was asked to
// store and returns it as product 1.
type fakeProducts struct {
	err error

	called bool
	got    NewProduct

	// product is what GetProduct returns; gotID is the id it was asked for.
	product Product
	gotID   int64
}

func (f *fakeProducts) CreateProduct(ctx context.Context, p NewProduct) (Product, error) {
	f.called = true
	f.got = p
	if f.err != nil {
		return Product{}, f.err
	}
	return Product{ID: 1, SKU: p.SKU, Name: p.Name, Description: p.Description, Category: p.Category, ReorderThreshold: p.ReorderThreshold, Active: true}, nil
}

func (f *fakeProducts) GetProduct(ctx context.Context, id int64) (Product, error) {
	f.called = true
	f.gotID = id
	if f.err != nil {
		return Product{}, f.err
	}
	return f.product, nil
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
			"zero threshold",
			NewProduct{SKU: "123456", Name: "Pallet jack", ReorderThreshold: ptr(0)},
			NewProduct{SKU: "123456", Name: "Pallet jack", ReorderThreshold: ptr(0)},
		},
		{
			"longest allowed fields",
			NewProduct{SKU: "123456", Name: strings.Repeat("é", 200), Description: strings.Repeat("é", 2000), Category: strings.Repeat("é", 100), ReorderThreshold: ptr(2147483647)},
			NewProduct{SKU: "123456", Name: strings.Repeat("é", 200), Description: strings.Repeat("é", 2000), Category: strings.Repeat("é", 100), ReorderThreshold: ptr(2147483647)},
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
		{"negative threshold", NewProduct{SKU: "123456", Name: "x", ReorderThreshold: ptr(-1)}, "reorder_threshold must be between 0 and 2147483647"},
		{"threshold too large", NewProduct{SKU: "123456", Name: "x", ReorderThreshold: ptr(2147483648)}, "reorder_threshold must be between 0 and 2147483647"},
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeProducts{}

			_, err := NewProductService(repo).CreateProduct(context.Background(), requester, tt.in)

			if !errors.Is(err, ErrForbidden) {
				t.Errorf("CreateProduct error = %v, want ErrForbidden", err)
			}
			if repo.called {
				t.Error("repository called for a non-admin")
			}
		})
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
		{"admin, active", admin, active, nil},
		{"admin, inactive", admin, inactive, nil},
		// Inactive products are hidden from everyone but admins.
		{"requester, inactive", requester, inactive, ErrNotFound},
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

func TestGetProductRepositoryError(t *testing.T) {
	for _, want := range []error{ErrNotFound, errors.New("db down")} {
		repo := &fakeProducts{err: errors.Join(errors.New("postgres: get product"), want)}

		_, err := NewProductService(repo).GetProduct(context.Background(), admin, 1)

		if !errors.Is(err, want) {
			t.Errorf("GetProduct error = %v, want %v", err, want)
		}
	}
}

func ptr(n int) *int { return &n }
