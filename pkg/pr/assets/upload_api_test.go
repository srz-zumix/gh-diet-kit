package assets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-github/v90/github"
	"github.com/srz-zumix/go-gh-extension/pkg/gh"
	ghclient "github.com/srz-zumix/go-gh-extension/pkg/gh/client"
)

// The upload endpoint's allowlist, request building and status handling live
// in go-gh-extension's gh package and are tested there; these tests cover the
// gh-diet-kit-specific uploader around them.

func writeAssetFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write asset file: %v", err)
	}
}

// newTestAPIUploader returns an uploader whose requests are rewritten to srv,
// so the real "uploads.<host>" URL building is exercised against a local
// server.
func newTestAPIUploader(t *testing.T, srv *httptest.Server) *APIUploader {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	httpClient := srv.Client()
	httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		return http.DefaultTransport.RoundTrip(req)
	})
	gc, err := github.NewClient(github.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("new go-github client: %v", err)
	}
	g, err := ghclient.NewClient(gc)
	if err != nil {
		t.Fatalf("new github client: %v", err)
	}
	return &APIUploader{g: g, host: "github.com", repositoryID: 1234}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPIUploaderUpload_Success(t *testing.T) {
	dir := t.TempDir()
	writeAssetFile(t, dir, "shot.png", "the bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://example.com/user-attachments/assets/1"}`))
	}))
	defer srv.Close()

	u := newTestAPIUploader(t, srv)
	assetURL, err := u.Upload(context.Background(), filepath.Join(dir, "shot.png"), "shot.png")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if assetURL != "https://example.com/user-attachments/assets/1" {
		t.Fatalf("Upload() = %q, want the asset url", assetURL)
	}
}

func TestAPIUploaderUpload_Unsupported(t *testing.T) {
	dir := t.TempDir()
	writeAssetFile(t, dir, "notes.pdf", "the bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Upload() sent a request for an unsupported file")
	}))
	defer srv.Close()

	u := newTestAPIUploader(t, srv)
	_, err := u.Upload(context.Background(), filepath.Join(dir, "notes.pdf"), "notes.pdf")
	if !errors.Is(err, gh.ErrUserAttachmentUnsupported) {
		t.Fatalf("Upload() error = %v, want gh.ErrUserAttachmentUnsupported", err)
	}
}

func TestAPIUploaderUpload_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeAssetFile(t, dir, "shot.png", "the bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	u := newTestAPIUploader(t, srv)
	_, err := u.Upload(context.Background(), filepath.Join(dir, "shot.png"), "shot.png")
	if err == nil || !strings.Contains(err.Error(), "write access") {
		t.Fatalf("Upload() error = %v, want the write-access message", err)
	}
}

func TestAPIUploaderUpload_RateLimitRetry(t *testing.T) {
	dir := t.TempDir()
	writeAssetFile(t, dir, "shot.png", "the bytes")

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://example.com/user-attachments/assets/1"}`))
	}))
	defer srv.Close()

	u := newTestAPIUploader(t, srv)
	assetURL, err := u.Upload(context.Background(), filepath.Join(dir, "shot.png"), "shot.png")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if assetURL == "" {
		t.Fatal("Upload() returned an empty asset url")
	}
}

func TestNewAPIUploader_RejectsEnterprise(t *testing.T) {
	if _, err := NewAPIUploader(nil, "ghe.example.com", 1234); err == nil {
		t.Fatal("NewAPIUploader() error = nil, want an error for a GHES host")
	}
}

func TestNewAPIUploader_RejectsMissingRepositoryID(t *testing.T) {
	if _, err := NewAPIUploader(nil, "github.com", 0); err == nil {
		t.Fatal("NewAPIUploader() error = nil, want an error for a zero repository id")
	}
}

func TestAllAssetsAPIUploadable(t *testing.T) {
	t.Run("all supported extensions and sizes", func(t *testing.T) {
		dir := t.TempDir()
		writeAssetFile(t, dir, "shot.png", "small")
		meta := &DumpMetadata{Assets: []*PRAsset{
			{Filename: "shot.png", LocalFile: "shot.png"},
		}}
		if !allAssetsAPIUploadable(meta, dir) {
			t.Fatal("allAssetsAPIUploadable() = false, want true")
		}
	})

	t.Run("unsupported extension blocks the API-only path", func(t *testing.T) {
		dir := t.TempDir()
		meta := &DumpMetadata{Assets: []*PRAsset{
			{Filename: "notes.pdf"},
		}}
		if allAssetsAPIUploadable(meta, dir) {
			t.Fatal("allAssetsAPIUploadable() = true, want false")
		}
	})

	t.Run("oversized file blocks the API-only path", func(t *testing.T) {
		dir := t.TempDir()
		big := make([]byte, gh.MaxUserAttachmentImageBytes+1)
		writeAssetFile(t, dir, "big.png", string(big))
		meta := &DumpMetadata{Assets: []*PRAsset{
			{Filename: "big.png", LocalFile: "big.png"},
		}}
		if allAssetsAPIUploadable(meta, dir) {
			t.Fatal("allAssetsAPIUploadable() = true, want false")
		}
	})

	t.Run("unresolvable local file does not block the API-only path", func(t *testing.T) {
		dir := t.TempDir()
		meta := &DumpMetadata{Assets: []*PRAsset{
			{Filename: "shot.png", LocalFile: "missing.png"},
		}}
		if !allAssetsAPIUploadable(meta, dir) {
			t.Fatal("allAssetsAPIUploadable() = false, want true")
		}
	})
}
