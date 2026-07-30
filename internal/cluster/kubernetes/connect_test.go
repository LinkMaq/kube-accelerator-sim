package kubernetes_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	clusterkubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/cluster/kubernetes"
)

func TestConnectRequiresAnExplicitKubeconfigAndContext(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "must-not-be-read"))

	for _, selection := range []cluster.TargetSelection{
		{},
		{KubeconfigPath: "/explicit/config"},
		{ContextName: "explicit-context"},
	} {
		if _, err := clusterkubernetes.Connect(
			context.Background(),
			selection,
		); err == nil {
			t.Fatalf("Connect(%#v) unexpectedly succeeded", selection)
		}
	}
}

func TestConnectPinsNamedContextAndReturnsRedactedTargetReceipt(t *testing.T) {
	const (
		contextName  = "explicit-context"
		namespaceUID = "6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c"
		token        = "target-secret-token"
	)
	server := newTargetServer(t, namespaceUID, token)
	certificate := pemCertificate(t, server)
	kubeconfigPath := writeKubeconfig(
		t,
		server.URL,
		certificate,
		contextName,
		token,
	)
	linkPath := filepath.Join(t.TempDir(), "target-config")
	if err := os.Symlink(kubeconfigPath, linkPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "ignored"))

	connection, err := clusterkubernetes.Connect(
		context.Background(),
		cluster.TargetSelection{
			KubeconfigPath: linkPath,
			ContextName:    contextName,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt := connection.Receipt()
	canonicalPath, err := filepath.EvalSymlinks(kubeconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPath, err = filepath.Abs(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContextName != contextName ||
		receipt.CanonicalKubeconfigPath != canonicalPath ||
		receipt.APIServerURL != server.URL ||
		receipt.TargetFingerprint.String() != targetDigest(namespaceUID) ||
		receipt.CADigest.String() != bytesDigest(certificate) {
		t.Fatalf("unexpected connection receipt: %#v", receipt)
	}
	if strings.Contains(fmt.Sprintf("%#v", receipt), token) {
		t.Fatal("connection receipt exposed bearer token")
	}
	if connection.Target().ContextName != contextName ||
		connection.Target().Fingerprint != receipt.TargetFingerprint {
		t.Fatal("explicit target and receipt identity diverged")
	}
	capabilities, err := connection.ControlPlane().Probe(
		context.Background(),
		connection.Target(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.TargetFingerprint != receipt.TargetFingerprint {
		t.Fatal("connected Scenario Control Plane lost target identity")
	}
}

func newTargetServer(t *testing.T, namespaceUID, token string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/api/v1/namespaces/kube-system" {
				http.NotFound(response, request)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+token {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(
				response,
				`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"kube-system","uid":%q}}`,
				namespaceUID,
			)
		},
	))
	t.Cleanup(server.Close)
	return server
}

func pemCertificate(t *testing.T, server *httptest.Server) []byte {
	t.Helper()
	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("TLS server has no certificate")
	}
	return []byte(fmt.Sprintf(
		"-----BEGIN CERTIFICATE-----\n%s\n-----END CERTIFICATE-----\n",
		base64.StdEncoding.EncodeToString(certificate.Raw),
	))
}

func writeKubeconfig(
	t *testing.T,
	serverURL string,
	certificate []byte,
	contextName, token string,
) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	contents := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
  - name: explicit-cluster
    cluster:
      server: %q
      certificate-authority-data: %s
  - name: wrong-cluster
    cluster:
      server: https://127.0.0.1:1
      insecure-skip-tls-verify: true
contexts:
  - name: %s
    context:
      cluster: explicit-cluster
      user: explicit-user
  - name: wrong-current
    context:
      cluster: wrong-cluster
      user: wrong-user
current-context: wrong-current
users:
  - name: explicit-user
    user:
      token: %q
  - name: wrong-user
    user:
      token: wrong
`, serverURL, base64.StdEncoding.EncodeToString(certificate), contextName, token)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func targetDigest(uid string) string {
	sum := sha256.Sum256([]byte("kasim.target-fingerprint.v1\x00" + uid))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func bytesDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
