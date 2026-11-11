package handler

// fork: host reboot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
	"github.com/PatchMon/PatchMon/server-source-code/internal/util"
)

func TestServeInstall_AllowReboot(t *testing.T) {
	d := testDatabase(t)
	hosts := store.NewHostsStore(d)
	host := newTestHost(t, hosts, "pending")
	t.Setenv("AI_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	enc, err := util.NewEncryption()
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	h := NewInstallHandler(hosts, store.NewSettingsStore(d), store.NewBootstrapStore(&hostctx.RedisResolver{Default: rdb}, enc), store.NewReportStore(d), nil)

	for query, want := range map[string]bool{"": false, "&allow_reboot=true": true, "&allow_reboot=1": true, "&allow_reboot=yes": false} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/hosts/install?os=linux"+query, nil)
		req.Header.Set("X-API-ID", host.ApiID)
		req.Header.Set("X-API-KEY", host.ApiKey)
		rec := httptest.NewRecorder()
		h.ServeInstall(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: %d %s", query, rec.Code, rec.Body.String())
		}
		if got := strings.Contains(rec.Body.String(), `export ALLOW_REBOOT="true"`); got != want {
			t.Errorf("%q: ALLOW_REBOOT exported = %v, want %v", query, got, want)
		}
	}
}
