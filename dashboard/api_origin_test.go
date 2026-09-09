package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOriginForRequest verifies that, with InterfaceURLDynamic enabled, the
// dashboard derives its public origin from the request Host (so the SPA talks
// to the API on the same origin it was loaded from), and otherwise falls back
// to the configured InterfaceURL.
func TestOriginForRequest(t *testing.T) {
	staticConf := &Config{InterfaceURL: "https://dashboard.demo.localflynn.com", InterfaceURLDynamic: false}
	dynConf := &Config{InterfaceURL: "https://dashboard.demo.localflynn.com", InterfaceURLDynamic: true}

	req := httptest.NewRequest("GET", "https://dashboard.flynn.lab.p22.de/config", nil)
	req.Host = "dashboard.flynn.lab.p22.de"
	req.Header.Set("X-Forwarded-Proto", "https")

	if got := (&API{conf: dynConf}).originForRequest(req); got != "https://dashboard.flynn.lab.p22.de" {
		t.Fatalf("dynamic origin = %q, want https://dashboard.flynn.lab.p22.de", got)
	}
	if got := (&API{conf: staticConf}).originForRequest(req); got != "https://dashboard.demo.localflynn.com" {
		t.Fatalf("static origin = %q, want https://dashboard.demo.localflynn.com", got)
	}
}

// TestCorsDynamicOrigin verifies that a CORS preflight from the same origin the
// dashboard is served on is allowed when InterfaceURLDynamic is enabled.
func TestCorsDynamicOrigin(t *testing.T) {
	api := &API{conf: &Config{InterfaceURL: "https://dashboard.demo.localflynn.com", InterfaceURLDynamic: true}}
	h := api.CorsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("OPTIONS", "https://dashboard.flynn.lab.p22.de/config", nil)
	req.Host = "dashboard.flynn.lab.p22.de"
	req.Header.Set("Origin", "https://dashboard.flynn.lab.p22.de")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.flynn.lab.p22.de" {
		t.Fatalf("ACAO = %q, want https://dashboard.flynn.lab.p22.de", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

// TestCSPDynamicOrigin verifies the CSP connect-src includes the request origin
// when InterfaceURLDynamic is enabled.
func TestCSPDynamicOrigin(t *testing.T) {
	conf := &Config{
		InterfaceURL:        "https://dashboard.demo.localflynn.com",
		InterfaceURLDynamic: true,
		ControllerDomain:    "controller.demo.localflynn.com",
		StatusDomain:        "status.demo.localflynn.com",
	}
	api := &API{conf: conf}
	h := api.ContentSecurityHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("GET", "https://dashboard.flynn.lab.p22.de/", nil)
	req.Host = "dashboard.flynn.lab.p22.de"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://dashboard.flynn.lab.p22.de") {
		t.Fatalf("CSP connect-src missing request origin: %q", csp)
	}
}

// TestServeDashboardJsDynamicAPIServer verifies the injected
// window.DashboardConfig.API_SERVER reflects the request origin when dynamic.
func TestServeDashboardJsDynamicAPIServer(t *testing.T) {
	api := &API{conf: &Config{
		InterfaceURL:        "https://dashboard.demo.localflynn.com",
		InterfaceURLDynamic: true,
		AppName:             "dashboard",
	}}
	// Seed the cached JS body so cacheDashboardJS is a no-op.
	api.dashboardJSBody = []byte("// app js\n")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "https://dashboard.flynn.lab.p22.de/assets/dashboard-x.js", nil)
	req.Host = "dashboard.flynn.lab.p22.de"
	req.Header.Set("X-Forwarded-Proto", "https")
	api.ServeDashboardJs(nil, rec, req)

	body := rec.Body.String()
	var cfg DashboardConfig
	prefix := "window.DashboardConfig = "
	if !strings.HasPrefix(body, prefix) {
		t.Fatalf("body missing DashboardConfig prefix")
	}
	jsonPart := body[len(prefix):strings.Index(body, ";\n")]
	if err := json.Unmarshal([]byte(jsonPart), &cfg); err != nil {
		t.Fatalf("unmarshal DashboardConfig: %v", err)
	}
	if cfg.ApiServer != "https://dashboard.flynn.lab.p22.de" {
		t.Fatalf("API_SERVER = %q, want https://dashboard.flynn.lab.p22.de", cfg.ApiServer)
	}
}
