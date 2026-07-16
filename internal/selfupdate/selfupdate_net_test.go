package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSha256Hex(t *testing.T) {
	got := sha256hex([]byte("hello"))
	sum := sha256.Sum256([]byte("hello"))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("sha256hex = %q, want %q", got, want)
	}
}

func TestFindAsset(t *testing.T) {
	rel := &ghRelease{Assets: []ghAsset{
		{Name: "tarjan.tar.gz", URL: "http://x/a"},
		{Name: "checksums.txt", URL: "http://x/sums"},
	}}
	if got := findAsset(rel, "checksums.txt"); got != "http://x/sums" {
		t.Errorf("findAsset(checksums) = %q, want http://x/sums", got)
	}
	if got := findAsset(rel, "missing"); got != "" {
		t.Errorf("findAsset(missing) = %q, want empty", got)
	}
}

func TestNotFoundHint(t *testing.T) {
	if err := notFoundHint("404 Not Found", ""); !errors.Is(err, ErrNoToken) {
		t.Errorf("no-token hint should wrap ErrNoToken, got %v", err)
	}
	if err := notFoundHint("404 Not Found", "tok"); errors.Is(err, ErrNoToken) {
		t.Error("with a token present, the hint must not blame a missing token")
	}
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinaryTarGz(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"README.md":  "docs",
		binaryName(): "BINARY-BYTES",
	})
	got, err := extractBinary(archive, false)
	if err != nil {
		t.Fatalf("extractBinary(tar.gz): %v", err)
	}
	if string(got) != "BINARY-BYTES" {
		t.Fatalf("extracted = %q, want BINARY-BYTES", got)
	}
}

func TestExtractBinaryTarGzMissing(t *testing.T) {
	archive := makeTarGz(t, map[string]string{"unrelated": "x"})
	if _, err := extractBinary(archive, false); err == nil {
		t.Fatal("extractBinary with no binary present = nil error, want error")
	}
}

func TestExtractBinaryZip(t *testing.T) {
	archive := makeZip(t, map[string]string{
		"README.md":  "docs",
		binaryName(): "ZIP-BINARY",
	})
	got, err := extractBinary(archive, true)
	if err != nil {
		t.Fatalf("extractBinary(zip): %v", err)
	}
	if string(got) != "ZIP-BINARY" {
		t.Fatalf("extracted = %q, want ZIP-BINARY", got)
	}
}

func TestExtractBinaryBadArchive(t *testing.T) {
	if _, err := extractBinary([]byte("not a gzip stream"), false); err == nil {
		t.Fatal("extractBinary on garbage = nil error, want error")
	}
	if _, err := extractBinary([]byte("not a zip"), true); err == nil {
		t.Fatal("extractBinary on garbage zip = nil error, want error")
	}
}

func TestFetchReleaseSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want Bearer tok", got)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","assets":[{"name":"a","url":"u"}]}`))
	}))
	defer srv.Close()

	rel, err := fetchRelease(context.Background(), srv.URL, "tok")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	if rel.TagName != "v1.2.3" || len(rel.Assets) != 1 {
		t.Fatalf("unexpected release: %+v", rel)
	}
}

func TestFetchRelease404WithoutTokenWrapsErrNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := fetchRelease(context.Background(), srv.URL, ""); !errors.Is(err, ErrNoToken) {
		t.Fatalf("404 without token should wrap ErrNoToken, got %v", err)
	}
}

func TestFetchReleaseServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := fetchRelease(context.Background(), srv.URL, "tok"); err == nil {
		t.Fatal("500 response = nil error, want error")
	}
}

func TestFetchReleaseMissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"assets":[]}`))
	}))
	defer srv.Close()

	if _, err := fetchRelease(context.Background(), srv.URL, "tok"); err == nil {
		t.Fatal("missing tag_name = nil error, want error")
	}
}

func TestDownloadAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/octet-stream" {
			t.Errorf("Accept = %q, want application/octet-stream", got)
		}
		_, _ = w.Write([]byte("ASSET-BYTES"))
	}))
	defer srv.Close()

	got, err := downloadAsset(context.Background(), srv.URL, "tok")
	if err != nil {
		t.Fatalf("downloadAsset: %v", err)
	}
	if string(got) != "ASSET-BYTES" {
		t.Fatalf("downloaded = %q, want ASSET-BYTES", got)
	}
}

func TestDownloadAssetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := downloadAsset(context.Background(), srv.URL, "tok"); err == nil {
		t.Fatal("403 response = nil error, want error")
	}
}
