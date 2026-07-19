// Package web implements the public site host (CMS): it serves user-authored
// HTML/CSS/JS pages from the sitio_web module at /site and /site/:slug, and a
// public products feed at /api/public/products for storefront pages.
//
// These endpoints are public (no auth). Page CRUD/editing is handled by the
// module's auto-generated authed API (/api/sitio_web/web_page).
package web

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fasterp/backend/internal/db"
)

const tPage = "mod_sitio_web_web_page"
const tMedia = "mod_sitio_web_media"

// MediaItem is an uploaded file tracked by the CMS.
type MediaItem struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	URL       string `json:"url"`
	Alt       string `json:"alt"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`
}

// Page is a stored web page.
type Page struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	HTML      string `json:"html"`
	CSS       string `json:"css"`
	JS        string `json:"js"`
	Published bool   `json:"published"`
	IsHome    bool   `json:"is_home"`
}

// ErrNotFound is returned when no published page matches.
var ErrNotFound = fmt.Errorf("not found")

func scanPage(row interface{ Scan(...interface{}) error }) (*Page, error) {
	var p Page
	var title, html, css, js sql.NullString
	var published, isHome sql.NullBool
	if err := row.Scan(&p.Slug, &title, &html, &css, &js, &published, &isHome); err != nil {
		return nil, err
	}
	p.Title, p.HTML, p.CSS, p.JS = title.String, html.String, css.String, js.String
	p.Published, p.IsHome = published.Bool, isHome.Bool
	return &p, nil
}

// GetHome returns the published home page (is_home = true, else lowest seq).
func GetHome(ctx context.Context, ex db.QueryExecutor, tenantID string) (*Page, error) {
	q := fmt.Sprintf(`SELECT slug, title, html, css, js, published, is_home FROM %s
		WHERE tenant_id = $1 AND published = true
		ORDER BY is_home DESC, seq ASC, created_at ASC LIMIT 1`, tPage)
	p, err := scanPage(ex.QueryRowContext(ctx, q, tenantID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return p, err
}

// GetBySlug returns a published page by slug.
func GetBySlug(ctx context.Context, ex db.QueryExecutor, tenantID, slug string) (*Page, error) {
	q := fmt.Sprintf(`SELECT slug, title, html, css, js, published, is_home FROM %s
		WHERE tenant_id = $1 AND slug = $2 AND published = true LIMIT 1`, tPage)
	p, err := scanPage(ex.QueryRowContext(ctx, q, tenantID, slug))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return p, err
}

// ListMedia returns all media items for a tenant, newest first.
func ListMedia(ctx context.Context, ex db.QueryExecutor, tenantID string) ([]MediaItem, error) {
	q := fmt.Sprintf(`SELECT id, COALESCE(filename,''), COALESCE(url,''), COALESCE(alt,''),
		COALESCE(mime_type,''), COALESCE(size_bytes,0), created_at
		FROM %s WHERE tenant_id = $1 ORDER BY created_at DESC`, tMedia)
	rows, err := ex.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []MediaItem
	for rows.Next() {
		var m MediaItem
		if err := rows.Scan(&m.ID, &m.Filename, &m.URL, &m.Alt, &m.MimeType, &m.SizeBytes, &m.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// AddMedia inserts a media record and returns it.
func AddMedia(ctx context.Context, ex db.QueryExecutor, tenantID, filename, url, alt, mimeType string, sizeBytes int64) (*MediaItem, error) {
	var m MediaItem
	q := fmt.Sprintf(`INSERT INTO %s (id, tenant_id, filename, url, alt, mime_type, size_bytes, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, now(), now())
		RETURNING id, filename, url, COALESCE(alt,''), COALESCE(mime_type,''), COALESCE(size_bytes,0), created_at`, tMedia)
	err := ex.QueryRowContext(ctx, q, tenantID, filename, url, alt, mimeType, sizeBytes).
		Scan(&m.ID, &m.Filename, &m.URL, &m.Alt, &m.MimeType, &m.SizeBytes, &m.CreatedAt)
	return &m, err
}

// DeleteMedia removes a media record and returns its URL for file cleanup.
func DeleteMedia(ctx context.Context, ex db.QueryExecutor, tenantID, id string) (string, error) {
	var url string
	q := fmt.Sprintf(`DELETE FROM %s WHERE id = $1 AND tenant_id = $2 RETURNING url`, tMedia)
	err := ex.QueryRowContext(ctx, q, id, tenantID).Scan(&url)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	return url, err
}
