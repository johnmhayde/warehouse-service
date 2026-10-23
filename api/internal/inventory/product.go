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

// Product is an entry in the product catalog, with the number of units the
// warehouse owns.
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
// active. TotalQuantity is the number of units the warehouse owns (0 if
// none yet). TotalQuantity only ever changes together with a stock
// adjustment, so when TotalQuantity is above 0 the repository also records
// an InitialStockReason adjustment of that many units by the creating user,
// in the same transaction; at 0 it records none.
type NewProduct struct {
	SKU           SKU
	Name          string
	Description   string
	Category      string
	TotalQuantity int
}

// ProductFilter selects one page of products for ListProducts.
type ProductFilter struct {
	// Query matches products whose name contains it, ignoring case. It is
	// trimmed; "" matches every name.
	Query string
	// Category matches products in exactly this category. It is trimmed;
	// "" matches every product, including uncategorized ones.
	Category string
	// IncludeInactive also lists inactive products. Only admins may set it.
	IncludeInactive bool
	// Limit is the most products to return, from 1 to MaxProductLimit.
	// Callers with no limit of their own use DefaultProductLimit.
	Limit int
	// After, if not nil, is a previous page's Next: the page starts after
	// that product.
	After *ProductCursor
}

// ProductCursor is a position in ListProducts' (name, id) order: the last
// product of a page.
type ProductCursor struct {
	Name string
	ID   int64
}

// ProductPage is one page of ListProducts.
type ProductPage struct {
	Products []Product
	// Next is where the next page starts, or nil on the last page.
	Next *ProductCursor
}

// Page sizes for ListProducts.
const (
	DefaultProductLimit = 50
	MaxProductLimit     = 100
)

// ProductUpdate is a change to a product's catalog details. A nil field is
// left as it is, and at least one field must be set. A Category that is
// empty or blank clears the category (uncategorized). The owned count
// (TotalQuantity) can't be changed here: it only changes through stock
// adjustments.
type ProductUpdate struct {
	Name        *string
	Description *string
	Category    *string
}

// Fields returns the products column names of the fields u sets, in a
// fixed order.
func (u ProductUpdate) Fields() []string {
	var fields []string
	if u.Name != nil {
		fields = append(fields, "name")
	}
	if u.Description != nil {
		fields = append(fields, "description")
	}
	if u.Category != nil {
		fields = append(fields, "category")
	}
	return fields
}

// Limits on product fields, in characters (runes). MaxNameLen is exported
// so the HTTP layer can bound the name inside a page cursor.
const (
	MaxNameLen        = 200
	maxDescriptionLen = 2000
	maxCategoryLen    = 100
)

// The products.total_quantity column is a 32-bit integer.
const maxTotalQuantity = math.MaxInt32

// InitialStockReason is the reason recorded on the stock adjustment that
// gives a new product its initial TotalQuantity.
const InitialStockReason = "Initial stock"

var (
	ErrNotFound     = errors.New("product not found")
	ErrDuplicateSKU = errors.New("product with this SKU already exists")
	ErrForbidden    = errors.New("admin role required")
	// ErrEmptyUpdate means a ProductUpdate sets no fields.
	ErrEmptyUpdate = errors.New("no fields to update")

	// ErrHidden means the product exists but the caller may not see it. It
	// wraps ErrNotFound, so callers that only check for ErrNotFound treat
	// it as a missing product.
	ErrHidden = fmt.Errorf("%w: inactive", ErrNotFound)
)

// ValidationError reports a product field or list filter that breaks a
// rule. Its message is safe to show to clients, e.g. "name is required".
// Field uses the products column names, or for a ProductFilter the query
// parameter names ("q", "category", "limit").
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Reason
}

// Actor is who a service call is made on behalf of. The HTTP layer builds
// it from the request's identity. The zero Actor is an anonymous caller:
// no user, and not an admin.
type Actor struct {
	UserID int64
	Admin  bool
}

// ProductRepository is what product management needs from storage.
type ProductRepository interface {
	// CreateProduct stores p and returns the new product, or an error
	// wrapping ErrDuplicateSKU if a product already has p.SKU. If
	// p.TotalQuantity is above 0 it also records, atomically with the
	// product, a stock adjustment of that many units with reason
	// InitialStockReason by the user actorID.
	CreateProduct(ctx context.Context, actorID int64, p NewProduct) (Product, error)
	// GetProduct returns the product with this id, active or not, or an
	// error wrapping ErrNotFound if there is none.
	GetProduct(ctx context.Context, id int64) (Product, error)
	// ListProducts returns up to f.Limit products matching f, ordered by
	// name and then id, starting after f.After. f is already normalized:
	// Query and Category are trimmed, and "" means no filter.
	ListProducts(ctx context.Context, f ProductFilter) ([]Product, error)
	// UpdateProduct applies u, which is normalized and sets at least one
	// field, to the product with this id, active or not, and returns the
	// updated product, or an error wrapping ErrNotFound if there is none.
	// It never changes TotalQuantity.
	UpdateProduct(ctx context.Context, id int64, u ProductUpdate) (Product, error)
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
	return s.repo.CreateProduct(ctx, actor.UserID, p)
}

// GetProduct returns the product with this id. Anyone may call it,
// including an anonymous (zero) actor. Inactive products are retired from
// normal use, so only admins see them: anyone else gets ErrHidden, which
// wraps ErrNotFound like an id that doesn't exist.
func (s *ProductService) GetProduct(ctx context.Context, actor Actor, id int64) (Product, error) {
	p, err := s.repo.GetProduct(ctx, id)
	if err != nil {
		return Product{}, err
	}
	if !p.Active && !actor.Admin {
		return Product{}, ErrHidden
	}
	return p, nil
}

// ListProducts returns one page of the catalog, ordered by name and then
// id. Anyone may call it, including an anonymous (zero) actor, but only
// admins may set f.IncludeInactive: anyone else gets ErrForbidden, checked
// before f is validated. The page's Next is nil on the last page.
func (s *ProductService) ListProducts(ctx context.Context, actor Actor, f ProductFilter) (ProductPage, error) {
	if f.IncludeInactive && !actor.Admin {
		return ProductPage{}, ErrForbidden
	}
	f, err := f.normalize()
	if err != nil {
		return ProductPage{}, err
	}
	// One extra row tells whether there is a next page.
	limit := f.Limit
	f.Limit++
	products, err := s.repo.ListProducts(ctx, f)
	if err != nil {
		return ProductPage{}, err
	}
	if len(products) <= limit {
		return ProductPage{Products: products}, nil
	}
	products = products[:limit]
	last := products[limit-1]
	return ProductPage{
		Products: products,
		Next:     &ProductCursor{Name: last.Name, ID: last.ID},
	}, nil
}

// UpdateProduct changes the name, description or category of the product
// with this id, leaving the fields u doesn't set as they are. Only admins
// may update products, inactive ones included; the role is checked before u
// is validated, so other callers learn nothing about the rules. Name and
// category are trimmed as in CreateProduct, and a blank category clears it.
func (s *ProductService) UpdateProduct(ctx context.Context, actor Actor, id int64, u ProductUpdate) (Product, error) {
	if !actor.Admin {
		return Product{}, ErrForbidden
	}
	u, err := u.normalize()
	if err != nil {
		return Product{}, err
	}
	return s.repo.UpdateProduct(ctx, id, u)
}

// normalize trims p's name and category and checks every field except the
// SKU, which ParseSKU has already checked.
func (p NewProduct) normalize() (NewProduct, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Category = strings.TrimSpace(p.Category)

	if p.Name == "" {
		return NewProduct{}, &ValidationError{"name", "is required"}
	}
	if p.TotalQuantity < 0 || p.TotalQuantity > maxTotalQuantity {
		return NewProduct{}, &ValidationError{"total_quantity", fmt.Sprintf("must be between 0 and %d", maxTotalQuantity)}
	}
	for _, err := range []error{
		checkText("name", p.Name, MaxNameLen),
		checkText("description", p.Description, maxDescriptionLen),
		checkText("category", p.Category, maxCategoryLen),
	} {
		if err != nil {
			return NewProduct{}, err
		}
	}
	return p, nil
}

// normalize trims f's query and category and checks them and the limit. A
// query longer than the longest allowed name could never match, so it has
// the same cap.
func (f ProductFilter) normalize() (ProductFilter, error) {
	f.Query = strings.TrimSpace(f.Query)
	f.Category = strings.TrimSpace(f.Category)

	if f.Limit < 1 || f.Limit > MaxProductLimit {
		return ProductFilter{}, &ValidationError{"limit", fmt.Sprintf("must be between 1 and %d", MaxProductLimit)}
	}
	for _, err := range []error{
		checkText("q", f.Query, MaxNameLen),
		checkText("category", f.Category, maxCategoryLen),
	} {
		if err != nil {
			return ProductFilter{}, err
		}
	}
	return f, nil
}

// normalize trims u's name and category and checks the fields u sets by
// the same rules as NewProduct.normalize.
func (u ProductUpdate) normalize() (ProductUpdate, error) {
	if u.Name == nil && u.Description == nil && u.Category == nil {
		return ProductUpdate{}, ErrEmptyUpdate
	}
	if u.Name != nil {
		name := strings.TrimSpace(*u.Name)
		if name == "" {
			return ProductUpdate{}, &ValidationError{"name", "must not be blank"}
		}
		if err := checkText("name", name, MaxNameLen); err != nil {
			return ProductUpdate{}, err
		}
		u.Name = &name
	}
	if u.Description != nil {
		if err := checkText("description", *u.Description, maxDescriptionLen); err != nil {
			return ProductUpdate{}, err
		}
	}
	if u.Category != nil {
		category := strings.TrimSpace(*u.Category)
		if err := checkText("category", category, maxCategoryLen); err != nil {
			return ProductUpdate{}, err
		}
		u.Category = &category
	}
	return u, nil
}

// checkText checks that a text field is at most maxLen characters and
// holds no NUL.
func checkText(field, value string, maxLen int) error {
	if utf8.RuneCountInString(value) > maxLen {
		return &ValidationError{field, fmt.Sprintf("must be at most %d characters", maxLen)}
	}
	// Postgres text can't hold NUL, so reject it here rather than fail in
	// the store.
	if strings.ContainsRune(value, 0) {
		return &ValidationError{field, "must not contain NUL characters"}
	}
	return nil
}
