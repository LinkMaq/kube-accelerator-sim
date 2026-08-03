package ui_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory/memory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/ui"
)

func TestLoopbackServerProtectsInventoryWithoutLeakingCapability(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	source := memory.New()
	module := inventory.New(source)
	server, err := ui.NewServer(context.Background(), ui.Options{
		Module: module,
		Target: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "lab",
		},
		Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve() shutdown: %v", err)
			}
		case <-time.After(7 * time.Second):
			t.Error("Serve() did not shut down")
		}
	})

	accessURL, err := url.Parse(server.AccessURL())
	if err != nil {
		t.Fatal(err)
	}
	if accessURL.Fragment == "" || accessURL.RawQuery != "" {
		t.Fatalf("access URL = %q, want fragment capability only", server.AccessURL())
	}
	token := strings.TrimPrefix(accessURL.Fragment, "token=")
	if len(token) < 43 {
		t.Fatalf("capability token has only %d base64url characters", len(token))
	}
	requestURL := "http://127.0.0.1:" + strconv.Itoa(port)

	root := request(t, http.MethodGet, requestURL+"/", "", "")
	if root.StatusCode != http.StatusOK {
		t.Fatalf("root status = %d", root.StatusCode)
	}
	rootBody, _ := io.ReadAll(root.Body)
	root.Body.Close()
	if strings.Contains(string(rootBody), "lab") || strings.Contains(string(rootBody), token) {
		t.Fatal("static root leaked target data or capability")
	}
	assertSecurityHeaders(t, root.Header)

	javascript := request(t, http.MethodGet, requestURL+"/assets/app.js", "", "")
	javascriptBody, _ := io.ReadAll(javascript.Body)
	javascript.Body.Close()
	if javascript.StatusCode != http.StatusOK || len(javascriptBody) > 64<<10 {
		t.Fatalf("embedded JavaScript status=%d size=%d", javascript.StatusCode, len(javascriptBody))
	}
	javascriptText := string(javascriptBody)
	for _, forbidden := range []string{"http://", "https://", "localStorage", "WebSocket"} {
		if strings.Contains(javascriptText, forbidden) {
			t.Fatalf("embedded JavaScript contains forbidden capability %q", forbidden)
		}
	}
	for _, required := range []string{
		"associations", "allocation", "popstate", "data-filter",
	} {
		if !strings.Contains(javascriptText, required) {
			t.Fatalf("embedded JavaScript lacks %q behavior", required)
		}
	}

	missing := request(t, http.MethodGet, requestURL+"/api/v1/snapshot", "", "")
	if missing.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", missing.StatusCode)
	}
	missing.Body.Close()

	query := request(t, http.MethodGet, requestURL+"/api/v1/snapshot?token="+token, "", "")
	if query.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token status = %d", query.StatusCode)
	}
	query.Body.Close()

	authorized := request(
		t,
		http.MethodGet,
		requestURL+"/api/v1/snapshot",
		"Bearer "+token,
		"https://other.invalid",
	)
	if authorized.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(authorized.Body)
		t.Fatalf("authorized status = %d: %s", authorized.StatusCode, body)
	}
	if authorized.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("server emitted a permissive CORS header")
	}
	var snapshot inventory.Snapshot
	if err := json.NewDecoder(authorized.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	authorized.Body.Close()
	if snapshot.Revision != 1 || snapshot.Completeness != inventory.CompletenessLoading {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	post := request(t, http.MethodPost, requestURL+"/api/v1/snapshot", "Bearer "+token, "")
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", post.StatusCode)
	}
	post.Body.Close()

	badHostRequest, err := http.NewRequest(http.MethodGet, requestURL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	badHostRequest.Host = "localhost:" + strconv.Itoa(port)
	badHost, err := http.DefaultClient.Do(badHostRequest)
	if err != nil {
		t.Fatal(err)
	}
	if badHost.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("bad Host status = %d", badHost.StatusCode)
	}
	badHost.Body.Close()
}

func request(
	t *testing.T,
	method string,
	requestURL string,
	authorization string,
	origin string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, requestURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertSecurityHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for name, want := range map[string]string{
		"Cache-Control":                "no-store",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	} {
		if header.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, header.Get(name), want)
		}
	}
	if !strings.Contains(header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", header.Get("Content-Security-Policy"))
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
