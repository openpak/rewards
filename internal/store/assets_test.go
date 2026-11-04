package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/openpak/rewards/internal/rules"
)

func TestAssetRoundTripAndRestart(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	itemFixture(t, s, "art-one", 10)
	a, err := s.FindAsset(ctx, "art-one", "", "asset")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("png-bytes"))
	if string(a.Data) != "png-bytes" || a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("round trip mismatch: %q %s", a.Data, a.SHA256)
	}
	if _, _, err := s.PutAsset(ctx, admin, "art-one", "web", "asset", "image/png", bytes.NewReader(make([]byte, MaxAssetBytes+1))); err == nil {
		t.Fatal("oversize asset accepted")
	}
	// A fresh store over the same database sees the earlier bytes.
	s2, err := Open(ctx, os.Getenv("REWARDS_TEST_DATABASE_URL"), t.TempDir(), rules.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	b, err := s2.FindAsset(ctx, "art-one", "", "preview")
	if err != nil || string(b.Data) != "preview-bytes" {
		t.Fatalf("after restart: %v %q", err, b.Data)
	}
}

func TestImportAssets(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	s.AssetDir = dir
	body := []byte("legacy file")
	sum := sha256.Sum256(body)
	name := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, ".upload-x"), []byte("tmp"), 0o600)
	os.WriteFile(filepath.Join(dir, "bad"+name[3:]), []byte("wrong hash"), 0o600)
	imp, _, err := s.ImportAssets(ctx)
	if err != nil || imp != 1 {
		t.Fatalf("first import: %d %v", imp, err)
	}
	if imp, _, err = s.ImportAssets(ctx); err != nil || imp != 0 {
		t.Fatalf("second import must be a no-op: %d %v", imp, err)
	}
	var got []byte
	if err := s.pool.QueryRow(ctx, `SELECT data FROM asset_blobs WHERE sha256=$1`, name).Scan(&got); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("imported bytes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal("import must not delete files")
	}
}
