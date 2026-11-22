package sessionimp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tg-matrix/sidecar/internal/sessionimp"
)

func TestSampleAccountImport(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "帐号", "tg-哥伦比亚-3-1009121231AwEOWFeduN", "573204554017")
	jb, err := os.ReadFile(filepath.Join(dir, "573204554017.json"))
	if err != nil {
		t.Skip(err)
	}
	sb, err := os.ReadFile(filepath.Join(dir, "573204554017.session"))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := sessionimp.ParseAccountJSON(jb)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "x.session")
	if err := os.WriteFile(tmp, sb, 0o600); err != nil {
		t.Fatal(err)
	}
	sq, err := sessionimp.ParseTelethonSQLiteFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sessionimp.BuildFromMetaAndSession(meta, sq, "573204554017")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.PreferredID != "573204554017" {
		t.Fatalf("id=%s", parsed.PreferredID)
	}
	if parsed.AppID != 8 {
		t.Fatalf("app=%d", parsed.AppID)
	}
	out, err := sessionimp.EncodeGotdSession(parsed.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 100 {
		t.Fatalf("gotd too small")
	}
	parsed2, err := sessionimp.BuildFromMetaAndSession(meta, nil, "573204554017")
	if err != nil {
		t.Fatal(err)
	}
	if parsed2.Data.DC != 1 {
		t.Fatalf("dc=%d", parsed2.Data.DC)
	}
}
