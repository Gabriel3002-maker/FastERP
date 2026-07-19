// Package store implements the "tienda web" (mini-store) backend: it merges the
// products synced from Odoo (imported_product) with local web-store overrides
// (name, description, price, published) and an Amazon-style image gallery.
//
// The tienda_web WASM module only declares the tables; file uploads and the
// merge queries live here because the WASI sandbox has no filesystem/DB access.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fasterp/backend/internal/db"
)

const (
	tImported = "mod_integracion_odoo_fasterp_imported_product"
	tProduct  = "mod_tienda_web_store_product"
	tImage    = "mod_tienda_web_store_image"
)

// ProductCard is a row for the storefront grid.
type ProductCard struct {
	OdooProductID int     `json:"odoo_product_id"`
	Name          string  `json:"name"`
	Sku           string  `json:"sku"`
	Category      string  `json:"category"`
	Price         float64 `json:"price"`
	Stock         float64 `json:"stock"`
	Image         string  `json:"image"`
	Published     bool    `json:"published"`
	Featured      bool    `json:"featured"`
	ImageCount    int     `json:"image_count"`
	HasOverride   bool    `json:"has_override"`
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
	OdooProductID int     `json:"odoo_product_id"`
	Name          string  `json:"name"`
	BaseName      string  `json:"base_name"`
	Sku           string  `json:"sku"`
	Category      string  `json:"category"`
	Description   string  `json:"description"`
	Price         float64 `json:"price"`
	BasePrice     float64 `json:"base_price"`
	Stock         float64 `json:"stock"`
	Published     bool    `json:"published"`
	Featured      bool    `json:"featured"`
	Images        []Image `json:"images"`
}

// UpsertInput carries the editable web-store fields.
type UpsertInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Published   bool    `json:"published"`
	Featured    bool    `json:"featured"`
}

func ns(v sql.NullString) string  { return v.String }
func nf(v sql.NullFloat64) float64 { return v.Float64 }

// ListProducts returns the storefront grid, merging Odoo base data with local
// overrides and the first gallery image (falling back to the Odoo image).
func ListProducts(ctx context.Context, ex db.QueryExecutor, tenantID string) ([]ProductCard, error) {
	q := fmt.Sprintf(`
		SELECT ip.odoo_product_id,
		       ip.name, ip.default_code, ip.category, ip.list_price, ip.qty_available, ip.image,
		       sp.id, sp.name, sp.price, sp.published, sp.featured,
		       (SELECT url FROM %[3]s si WHERE si.odoo_product_id = ip.odoo_product_id
		            AND si.tenant_id = $1 ORDER BY position ASC, created_at ASC LIMIT 1),
		       (SELECT COUNT(*) FROM %[3]s si WHERE si.odoo_product_id = ip.odoo_product_id
		            AND si.tenant_id = $1)
		FROM %[1]s ip
		LEFT JOIN %[2]s sp ON sp.odoo_product_id = ip.odoo_product_id AND sp.tenant_id = $1
		WHERE ip.tenant_id = $1
		ORDER BY sp.featured DESC NULLS LAST, ip.name ASC`, tImported, tProduct, tImage)

	rows, err := ex.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ProductCard, 0)
	for rows.Next() {
		var (
			odooID                             int
			baseName, sku, cat, baseImg        sql.NullString
			listPrice, qty                     sql.NullFloat64
			spID, ovName                       sql.NullString
			ovPrice                            sql.NullFloat64
			published, featured                sql.NullBool
			mainImg                            sql.NullString
			imgCount                           int
		)
		if err := rows.Scan(&odooID, &baseName, &sku, &cat, &listPrice, &qty, &baseImg,
			&spID, &ovName, &ovPrice, &published, &featured, &mainImg, &imgCount); err != nil {
			return nil, err
		}
		name := ns(baseName)
		if ovName.Valid && ovName.String != "" {
			name = ovName.String
		}
		price := nf(listPrice)
		if ovPrice.Valid {
			price = ovPrice.Float64
		}
		image := ns(baseImg)
		if mainImg.Valid && mainImg.String != "" {
			image = mainImg.String
		}
		out = append(out, ProductCard{
			OdooProductID: odooID, Name: name, Sku: ns(sku), Category: ns(cat),
			Price: price, Stock: nf(qty), Image: image,
			Published: published.Bool, Featured: featured.Bool,
			ImageCount: imgCount, HasOverride: spID.Valid,
		})
	}
	return out, rows.Err()
}

// GetProduct returns one product's editable detail plus its image gallery.
func GetProduct(ctx context.Context, ex db.QueryExecutor, tenantID string, odooID int) (*ProductDetail, error) {
	q := fmt.Sprintf(`
		SELECT ip.name, ip.default_code, ip.category, ip.list_price, ip.qty_available,
		       sp.name, sp.description, sp.price, sp.published, sp.featured
		FROM %[1]s ip
		LEFT JOIN %[2]s sp ON sp.odoo_product_id = ip.odoo_product_id AND sp.tenant_id = $1
		WHERE ip.odoo_product_id = $2 AND ip.tenant_id = $1`, tImported, tProduct)

	var (
		baseName, sku, cat, ovName, ovDesc sql.NullString
		listPrice, qty, ovPrice            sql.NullFloat64
		published, featured                sql.NullBool
	)
	err := ex.QueryRowContext(ctx, q, tenantID, odooID).Scan(
		&baseName, &sku, &cat, &listPrice, &qty, &ovName, &ovDesc, &ovPrice, &published, &featured)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	d := &ProductDetail{
		OdooProductID: odooID,
		BaseName:      ns(baseName),
		Name:          ns(baseName),
		Sku:           ns(sku),
		Category:      ns(cat),
		Description:   ns(ovDesc),
		BasePrice:     nf(listPrice),
		Price:         nf(listPrice),
		Stock:         nf(qty),
		Published:     published.Bool,
		Featured:      featured.Bool,
	}
	if ovName.Valid && ovName.String != "" {
		d.Name = ovName.String
	}
	if ovPrice.Valid {
		d.Price = ovPrice.Float64
	}

	imgs, err := listImages(ctx, ex, tenantID, odooID)
	if err != nil {
		return nil, err
	}
	d.Images = imgs
	return d, nil
}

func listImages(ctx context.Context, ex db.QueryExecutor, tenantID string, odooID int) ([]Image, error) {
	q := fmt.Sprintf(`SELECT id, url, position, alt FROM %s
		WHERE odoo_product_id = $1 AND tenant_id = $2
		ORDER BY position ASC, created_at ASC`, tImage)
	rows, err := ex.QueryContext(ctx, q, odooID, tenantID)
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

// productExists checks the product was synced from Odoo for this tenant.
func productExists(ctx context.Context, ex db.QueryExecutor, tenantID string, odooID int) (bool, error) {
	var one int
	err := ex.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT 1 FROM %s WHERE odoo_product_id = $1 AND tenant_id = $2 LIMIT 1`, tImported),
		odooID, tenantID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// UpsertProduct creates or updates the web-store override for a product.
func UpsertProduct(ctx context.Context, ex db.QueryExecutor, tenantID string, odooID int, in UpsertInput) error {
	exists, err := productExists(ctx, ex, tenantID, odooID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}

	var id string
	err = ex.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT id FROM %s WHERE odoo_product_id = $1 AND tenant_id = $2`, tProduct),
		odooID, tenantID).Scan(&id)

	if err == sql.ErrNoRows {
		_, err = ex.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
			(tenant_id, odoo_product_id, name, description, price, published, featured)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, tProduct),
			tenantID, odooID, in.Name, in.Description, in.Price, in.Published, in.Featured)
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
func AddImage(ctx context.Context, ex db.QueryExecutor, tenantID string, odooID int, url, alt string) (Image, error) {
	exists, err := productExists(ctx, ex, tenantID, odooID)
	if err != nil {
		return Image{}, err
	}
	if !exists {
		return Image{}, ErrNotFound
	}

	var nextPos int
	_ = ex.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COALESCE(MAX(position),-1)+1 FROM %s WHERE odoo_product_id=$1 AND tenant_id=$2`, tImage),
		odooID, tenantID).Scan(&nextPos)

	var id string
	err = ex.QueryRowContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(tenant_id, odoo_product_id, url, position, alt) VALUES ($1,$2,$3,$4,$5) RETURNING id`, tImage),
		tenantID, odooID, url, nextPos, alt).Scan(&id)
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
