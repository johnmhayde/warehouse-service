package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Product is an entry in the product catalog, with its current stock on
// hand.
type Product struct {
	ID          int64
	SKU         SKU
	Name        string
	Description string
	// Category is "" when the product is uncategorized.
	Category      string
	TotalQuantity int
	Active        bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewProduct is what's needed to create a product. A new product starts
// active with no stock: TotalQuantity only ever changes together with a
// stock adjustment.
type NewProduct struct {
	SKU         SKU
	Name        string
	Description string
	Category    string
}

// Limits on product fields, in characters (runes).
const (
	maxNameLen        = 200
	maxDescriptionLen = 2000
	maxCategoryLen    = 100
)

var (
	ErrDuplicateSKU = errors.New("product with this SKU already exists")
	ErrForbidden    = errors.New("admin role required")
)

// ValidationError reports a product field that breaks a rule. Its message
// is safe to show to clients, e.g. "name is required". Field uses the
// products column names.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Reason
}

// Actor is who a service call is made on behalf of. The HTTP layer builds
// it from the request's identity.
type Actor struct {
	UserID int64
	Admin  bool
}

// ProductRepository is what product management needs from storage.
type ProductRepository interface {
	// CreateProduct stores p and returns the new product, or an error
	// wrapping ErrDuplicateSKU if a product already has p.SKU.
	CreateProduct(ctx context.Context, p NewProduct) (Product, error)
}

// ProductService holds the product catalog's business logic.
type ProductService struct {
	repo ProductRepository
}

func NewProductService(repo ProductRepository) *ProductService {
	return &ProductService{repo: repo}
}

// CreateProduct adds p to the catalog. Only admins may create products; the
// role is checked before p is validated, so other callers learn nothing
// about the rules. Name and category are trimmed, and a blank category
// means uncategorized.
func (s *ProductService) CreateProduct(ctx context.Context, actor Actor, p NewProduct) (Product, error) {
	if !actor.Admin {
		return Product{}, ErrForbidden
	}
	p, err := p.normalize()
	if err != nil {
		return Product{}, err
	}
	return s.repo.CreateProduct(ctx, p)
}

// normalize trims p's name and category and checks every field except the
// SKU, which ParseSKU has already checked.
func (p NewProduct) normalize() (NewProduct, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Category = strings.TrimSpace(p.Category)

	if p.Name == "" {
		return NewProduct{}, &ValidationError{"name", "is required"}
	}
	for _, f := range []struct {
		name   string
		value  string
		maxLen int
	}{
		{"name", p.Name, maxNameLen},
		{"description", p.Description, maxDescriptionLen},
		{"category", p.Category, maxCategoryLen},
	} {
		if utf8.RuneCountInString(f.value) > f.maxLen {
			return NewProduct{}, &ValidationError{f.name, fmt.Sprintf("must be at most %d characters", f.maxLen)}
		}
		// Postgres text can't hold NUL, so reject it here rather than fail
		// in the store.
		if strings.ContainsRune(f.value, 0) {
			return NewProduct{}, &ValidationError{f.name, "must not contain NUL characters"}
		}
	}
	return p, nil
}
