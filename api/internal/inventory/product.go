package inventory

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	// ReorderThreshold is nil when the product has no threshold.
	ReorderThreshold *int
	Active           bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// NewProduct is what's needed to create a product. A new product starts
// active with no stock: TotalQuantity only ever changes together with a
// stock adjustment.
type NewProduct struct {
	SKU              SKU
	Name             string
	Description      string
	Category         string
	ReorderThreshold *int
}

// Limits on product fields, in characters (runes).
const (
	maxNameLen        = 200
	maxDescriptionLen = 2000
	maxCategoryLen    = 100
	// The products.reorder_threshold column is a 32-bit integer.
	maxReorderThreshold = math.MaxInt32
)

var (
	ErrNotFound     = errors.New("product not found")
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
	// GetProduct returns the product with this id, active or not, or an
	// error wrapping ErrNotFound if there is none.
	GetProduct(ctx context.Context, id int64) (Product, error)
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

// GetProduct returns the product with this id. Inactive products are
// retired from normal use, so only admins see them: anyone else gets
// ErrNotFound, the same as for an id that doesn't exist.
func (s *ProductService) GetProduct(ctx context.Context, actor Actor, id int64) (Product, error) {
	p, err := s.repo.GetProduct(ctx, id)
	if err != nil {
		return Product{}, err
	}
	if !p.Active && !actor.Admin {
		return Product{}, ErrNotFound
	}
	return p, nil
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
	if t := p.ReorderThreshold; t != nil && (*t < 0 || *t > maxReorderThreshold) {
		return NewProduct{}, &ValidationError{"reorder_threshold", fmt.Sprintf("must be between 0 and %d", maxReorderThreshold)}
	}
	return p, nil
}
