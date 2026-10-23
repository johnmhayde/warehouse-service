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
}

func (f *fakeProducts) CreateProduct(ctx context.Context, p NewProduct) (Product, error) {
	f.called = true
	f.got = p
	if f.err != nil {
		return Product{}, f.err
	}
	return Product{ID: 1, SKU: p.SKU, Name: p.Name, Description: p.Description, Category: p.Category, Active: true}, nil
}

var admin = Actor{UserID: 1, Admin: true}

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

			_, err := NewProductService(repo).CreateProduct(context.Background(), Actor{UserID: 2}, tt.in)

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
