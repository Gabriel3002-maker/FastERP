// Package store implements the "tienda web" (mini-store) backend: it merges the
// product catalogue with local web-store overrides (name, description, price,
// published) and an Amazon-style image gallery.
//
// The tienda_web WASM module only declares the tables; file uploads and the
// merge queries live here because the WASI sandbox has no filesystem/DB access.
//
// The catalogue is the source of truth for product identity. Overrides hang off
// products.product by UUID, so the storefront never depends on where the
// catalogue data originally came from.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fasterp/backend/internal/db"
)

const (
	tCatalog = "mod_products_product"
	tProduct = "mod_tienda_web_store_product"
	tImage   = "mod_tienda_web_store_image"
)

// ProductCard is a row for the storefront grid.
type ProductCard struct {
	ProductID   string  `json:"product_id"`
	Name        string  `json:"name"`
	Sku         string  `json:"sku"`
	Category    string  `json:"category"`
	Price       float64 `json:"price"`
	Stock       float64 `json:"stock"`
	Image       string  `json:"image"`
	Published   bool    `json:"published"`
	Featured    bool    `json:"featured"`
	ImageCount  int     `json:"image_count"`
	HasOverride bool    `json:"has_override"`
}

// Image is one gallery image.
type Image struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Position int    `json:"position"`
	Alt      string `json:"alt"`
}

// ProductDetail is the full editor payload for one product.
type ProductDetail struct {
	ProductID   string  `json:"product_id"`
	Name        string  `json:"name"`
	BaseName    string  `json:"base_name"`
	Sku         string  `json:"sku"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	BasePrice   float64 `json:"base_price"`
	Stock       float64 `json:"stock"`
	Published   bool    `json:"published"`
	Featured    bool    `json:"featured"`
	Images      []Image `json:"images"`
}

// UpsertInput carries the editable web-store fields.
type UpsertInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Published   bool    `json:"published"`
	Featured    bool    `json:"featured"`
}

func ns(v sql.NullString) string   { return v.String }
func nf(v sql.NullFloat64) float64 { return v.Float64 }

// ListProducts returns the storefront grid: catalogue data merged with local
// overrides, plus the first gallery image as the thumbnail.
func ListProducts(ctx context.Context, ex db.QueryExecutor, tenantID string) ([]ProductCard, error) {
	q := fmt.Sprintf(`
		SELECT p.id,
		       p.name, p.model, p.category, p.price, p.stock,
		       sp.id, sp.name, sp.price, sp.published, sp.featured,
		       (SELECT url FROM %[3]s si WHERE si.product_id = p.id
		            AND si.tenant_id = $1 ORDER BY position ASC, created_at ASC LIMIT 1),
		       (SELECT COUNT(*) FROM %[3]s si WHERE si.product_id = p.id
		            AND si.tenant_id = $1)
		FROM %[1]s p
		LEFT JOIN %[2]s sp ON sp.product_id = p.id AND sp.tenant_id = $1
		WHERE p.tenant_id = $1
		ORDER BY sp.featured DESC NULLS LAST, p.name ASC`, tCatalog, tProduct, tImage)

	rows, err := ex.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ProductCard, 0)
	for rows.Next() {
		var (
			productID           string
			baseName, sku, cat  sql.NullString
			basePrice           sql.NullFloat64
			stock               sql.NullInt64
			spID, ovName        sql.NullString
			ovPrice             sql.NullFloat64
			published, featured sql.NullBool
			mainImg             sql.NullString
			imgCount            int
		)
		if err := rows.Scan(&productID, &baseName, &sku, &cat, &basePrice, &stock,
			&spID, &ovName, &ovPrice, &published, &featured, &mainImg, &imgCount); err != nil {
			return nil, err
		}
		name := ns(baseName)
		if ovName.Valid && ovName.String != "" {
			name = ovName.String
		}
		price := nf(basePrice)
		if ovPrice.Valid {
			price = ovPrice.Float64
		}
		out = append(out, ProductCard{
			ProductID: productID, Name: name, Sku: ns(sku), Category: ns(cat),
			Price: price, Stock: float64(stock.Int64), Image: ns(mainImg),
			Published: published.Bool, Featured: featured.Bool,
			ImageCount: imgCount, HasOverride: spID.Valid,
		})
	}
	return out, rows.Err()
}

// GetProduct returns one product's editable detail plus its image gallery.
func GetProduct(ctx context.Context, ex db.QueryExecutor, tenantID, productID string) (*ProductDetail, error) {
	q := fmt.Sprintf(`
		SELECT p.name, p.model, p.category, p.price, p.stock,
		       sp.name, sp.description, sp.price, sp.published, sp.featured
		FROM %[1]s p
		LEFT JOIN %[2]s sp ON sp.product_id = p.id AND sp.tenant_id = $1
		WHERE p.id = $2 AND p.tenant_id = $1`, tCatalog, tProduct)

	var (
		baseName, sku, cat, ovName, ovDesc sql.NullString
		basePrice, ovPrice                 sql.NullFloat64
		stock                              sql.NullInt64
		published, featured                sql.NullBool
	)
	err := ex.QueryRowContext(ctx, q, tenantID, productID).Scan(
		&baseName, &sku, &cat, &basePrice, &stock, &ovName, &ovDesc, &ovPrice, &published, &featured)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	d := &ProductDetail{
		ProductID:   productID,
		BaseName:    ns(baseName),
		Name:        ns(baseName),
		Sku:         ns(sku),
		Category:    ns(cat),
		Description: ns(ovDesc),
		BasePrice:   nf(basePrice),
		Price:       nf(basePrice),
		Stock:       float64(stock.Int64),
		Published:   published.Bool,
		Featured:    featured.Bool,
	}
	if ovName.Valid && ovName.String != "" {
		d.Name = ovName.String
	}
	if ovPrice.Valid {
		d.Price = ovPrice.Float64
	}

	imgs, err := listImages(ctx, ex, tenantID, productID)
	if err != nil {
		return nil, err
	}
	d.Images = imgs
	return d, nil
}

func listImages(ctx context.Context, ex db.QueryExecutor, tenantID, productID string) ([]Image, error) {
	q := fmt.Sprintf(`SELECT id, url, position, alt FROM %s
		WHERE product_id = $1 AND tenant_id = $2
		ORDER BY position ASC, created_at ASC`, tImage)
	rows, err := ex.QueryContext(ctx, q, productID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Image, 0)
	for rows.Next() {
		var im Image
		var pos sql.NullInt64
		var alt sql.NullString
		if err := rows.Scan(&im.ID, &im.URL, &pos, &alt); err != nil {
			return nil, err
		}
		im.Position, im.Alt = int(pos.Int64), alt.String
		out = append(out, im)
	}
	return out, rows.Err()
}

// productExists checks the product is in the catalogue for this tenant.
func productExists(ctx context.Context, ex db.QueryExecutor, tenantID, productID string) (bool, error) {
	var one int
	err := ex.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT 1 FROM %s WHERE id = $1 AND tenant_id = $2 LIMIT 1`, tCatalog),
		productID, tenantID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// UpsertProduct creates or updates the web-store override for a product.
func UpsertProduct(ctx context.Context, ex db.QueryExecutor, tenantID, productID string, in UpsertInput) error {
	exists, err := productExists(ctx, ex, tenantID, productID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}

	var id string
	err = ex.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT id FROM %s WHERE product_id = $1 AND tenant_id = $2`, tProduct),
		productID, tenantID).Scan(&id)

	if err == sql.ErrNoRows {
		_, err = ex.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
			(tenant_id, product_id, name, description, price, published, featured)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, tProduct),
			tenantID, productID, in.Name, in.Description, in.Price, in.Published, in.Featured)
		return err
	}
	if err != nil {
		return err
	}
	_, err = ex.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET
		name=$1, description=$2, price=$3, published=$4, featured=$5, updated_at=NOW()
		WHERE id=$6 AND tenant_id=$7`, tProduct),
		in.Name, in.Description, in.Price, in.Published, in.Featured, id, tenantID)
	return err
}

// AddImage appends an image to a product's gallery.
func AddImage(ctx context.Context, ex db.QueryExecutor, tenantID, productID, url, alt string) (Image, error) {
	exists, err := productExists(ctx, ex, tenantID, productID)
	if err != nil {
		return Image{}, err
	}
	if !exists {
		return Image{}, ErrNotFound
	}

	var nextPos int
	_ = ex.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COALESCE(MAX(position),-1)+1 FROM %s WHERE product_id=$1 AND tenant_id=$2`, tImage),
		productID, tenantID).Scan(&nextPos)

	var id string
	err = ex.QueryRowContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(tenant_id, product_id, url, position, alt) VALUES ($1,$2,$3,$4,$5) RETURNING id`, tImage),
		tenantID, productID, url, nextPos, alt).Scan(&id)
	if err != nil {
		return Image{}, err
	}
	return Image{ID: id, URL: url, Position: nextPos, Alt: alt}, nil
}

// DeleteImage removes a gallery image and returns its URL so the caller can
// delete the underlying file.
func DeleteImage(ctx context.Context, ex db.QueryExecutor, tenantID, imageID string) (string, error) {
	var url string
	err := ex.QueryRowContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE id=$1 AND tenant_id=$2 RETURNING url`, tImage),
		imageID, tenantID).Scan(&url)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	return url, err
}

// ErrNotFound is returned when a product or image does not exist for the tenant.
var ErrNotFound = fmt.Errorf("not found")
