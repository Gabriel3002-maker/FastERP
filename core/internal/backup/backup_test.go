package backup

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeZip(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	return zr
}

func TestLoadManifestValidaVersion(t *testing.T) {
	zr := makeZip(t, map[string]string{
		"manifest.json": `{"app_version":"0.9","tenant_id":"x"}`,
	})
	if _, err := LoadManifest(zr); err == nil || !strings.Contains(err.Error(), "no compatible") {
		t.Fatalf("esperado error de versión, got %v", err)
	}
}

func TestExtractResourcesRechazaZipSlip(t *testing.T) {
	dir := t.TempDir()
	zr := makeZip(t, map[string]string{
		"resources/../evil.txt": "x",
	})
	if _, err := ExtractResources(zr, dir); err == nil {
		t.Fatal("debería rechazar una ruta con zip-slip")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("el zip-slip llegó al disco")
	}
}

func TestExtractResourcesOK(t *testing.T) {
	dir := t.TempDir()
	zr := makeZip(t, map[string]string{
		"resources/media/a.txt": "hola",
	})
	n, err := ExtractResources(zr, dir)
	if err != nil || n != 1 {
		t.Fatalf("got %d %v", n, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "media", "a.txt"))
	if string(b) != "hola" {
		t.Fatalf("contenido %q", b)
	}
}

func TestRewriteTenantID(t *testing.T) {
	in := "INSERT INTO tenants VALUES ('aaa', 'x');\nINSERT INTO users VALUES ('u', 'aaa');"
	out := RewriteTenantID(in, "aaa", "bbb")
	if strings.Contains(out, "aaa") || !strings.Contains(out, "bbb") {
		t.Fatalf("rewrite mal: %s", out)
	}
}
