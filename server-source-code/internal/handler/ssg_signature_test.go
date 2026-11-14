package handler

// fork: signed SCAP content

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

func TestSSGSignature(t *testing.T) {
	d := testDatabase(t)
	hosts := store.NewHostsStore(d)
	host := newTestHost(t, hosts, "active")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssg-debian12-ds.xml.minisig"), []byte("sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewComplianceHandler(nil, hosts, nil, nil, nil, nil, dir, nil, nil)

	get := func(filename string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/compliance/ssg-signature/"+filename, nil)
		if auth {
			req.Header.Set("X-API-ID", host.ApiID)
			req.Header.Set("X-API-KEY", host.ApiKey)
		}
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("filename", filename)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.SSGSignature(rec, req)
		return rec
	}

	if rec := get("ssg-debian12-ds.xml", true); rec.Code != http.StatusOK || rec.Body.String() != "sig" {
		t.Fatalf("existing signature: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("ssg-rhel9-ds.xml", true); rec.Code != http.StatusNotFound {
		t.Fatalf("missing signature: %d", rec.Code)
	}
	if rec := get("..%2F..%2Fetc%2Fpasswd", true); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid filename: %d", rec.Code)
	}
	if rec := get("ssg-debian12-ds.xml", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", rec.Code)
	}
}
