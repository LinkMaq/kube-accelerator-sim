// Package kubernetes implements explicit Simulation Target loading and the
// production Kubernetes Cluster adapter.
package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	controlplanekubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const targetFingerprintDomain = "kasim.target-fingerprint.v1"

// Connection holds clients configured once for one explicit context. It
// exposes only intention-level ports and redacted target identity.
type Connection struct {
	receipt      cluster.ConnectionReceipt
	target       controlplane.ExplicitTarget
	controlPlane controlplane.ScenarioControlPlane
	clusterPort  cluster.Port
}

// Connect loads exactly one named context, verifies authenticated TLS by
// reading kube-system, and freezes all clients for the returned connection.
func Connect(
	ctx context.Context,
	selection cluster.TargetSelection,
) (*Connection, error) {
	if selection.KubeconfigPath == "" || selection.ContextName == "" {
		return nil, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"explicit kubeconfig path and context name are both required",
			false,
		)
	}
	canonicalPath, err := canonicalKubeconfigPath(selection.KubeconfigPath)
	if err != nil {
		return nil, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			err.Error(),
			false,
		)
	}
	rawConfig, err := clientcmd.LoadFromFile(canonicalPath)
	if err != nil {
		return nil, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"load explicit kubeconfig failed",
			false,
		)
	}
	if _, found := rawConfig.Contexts[selection.ContextName]; !found {
		return nil, cluster.NewError(
			cluster.ErrorInvalidIntent,
			fmt.Sprintf(
				"explicit context %q does not exist in the selected kubeconfig",
				selection.ContextName,
			),
			false,
		)
	}
	restConfig, err := clientcmd.NewNonInteractiveClientConfig(
		*rawConfig,
		selection.ContextName,
		&clientcmd.ConfigOverrides{CurrentContext: selection.ContextName},
		nil,
	).ClientConfig()
	if err != nil {
		return nil, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"load explicit context failed",
			false,
		)
	}
	if err := rest.LoadTLSFiles(restConfig); err != nil {
		return nil, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"load explicit target TLS files failed",
			false,
		)
	}
	apiServerURL, err := canonicalAPIServerURL(restConfig.Host)
	if err != nil {
		return nil, cluster.NewError(
			cluster.ErrorInvalidIntent,
			err.Error(),
			false,
		)
	}
	if restConfig.Insecure || len(restConfig.CAData) == 0 {
		return nil, cluster.NewError(
			cluster.ErrorAuthenticationFailed,
			"explicit target requires verified TLS and cluster CA data",
			false,
		)
	}
	restConfig.UserAgent = "kube-accelerator-sim/dev"

	coreClient, err := clientset.NewForConfig(rest.CopyConfig(restConfig))
	if err != nil {
		return nil, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"construct explicit target client failed",
			false,
		)
	}
	namespace, err := coreClient.CoreV1().
		Namespaces().
		Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return nil, classify(
			"authenticate explicit target and read kube-system identity",
			err,
		)
	}
	if namespace.UID == "" {
		return nil, cluster.NewError(
			cluster.ErrorCapabilityUnavailable,
			"kube-system Namespace has no server-assigned UID",
			false,
		)
	}
	targetFingerprint, err := parseDigest(
		digestDomainValue(targetFingerprintDomain, string(namespace.UID)),
	)
	if err != nil {
		return nil, err
	}
	caDigest, err := parseDigest(digestBytes(restConfig.CAData))
	if err != nil {
		return nil, err
	}

	kubernetesClient, err := newControlPlaneClient(restConfig)
	if err != nil {
		return nil, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"construct Scenario Control Plane client failed",
			false,
		)
	}
	target := controlplane.ExplicitTarget{
		ContextName: selection.ContextName,
		Fingerprint: targetFingerprint,
	}
	return &Connection{
		receipt: cluster.ConnectionReceipt{
			ContextName:             selection.ContextName,
			CanonicalKubeconfigPath: canonicalPath,
			APIServerURL:            apiServerURL,
			TargetFingerprint:       targetFingerprint,
			CADigest:                caDigest,
		},
		target: target,
		controlPlane: controlplanekubernetes.New(
			kubernetesClient,
			"kubernetes-v1alpha1",
		),
		clusterPort: NewAdapter(coreClient),
	}, nil
}

// Receipt returns a copy containing no credentials or raw kubeconfig data.
func (connection *Connection) Receipt() cluster.ConnectionReceipt {
	return connection.receipt
}

func (connection *Connection) Target() controlplane.ExplicitTarget {
	return connection.target
}

func (connection *Connection) ControlPlane() controlplane.ScenarioControlPlane {
	return connection.controlPlane
}

func (connection *Connection) Cluster() cluster.Port {
	return connection.clusterPort
}

func canonicalKubeconfigPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve explicit kubeconfig path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve explicit kubeconfig symlinks: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect explicit kubeconfig: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("explicit kubeconfig must be one regular file")
	}
	return canonical, nil
}

func canonicalAPIServerURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse explicit API server URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf(
			"explicit API server URL must be a credential-free HTTPS URL",
		)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String(), nil
}

func newControlPlaneClient(config *rest.Config) (client.WithWatch, error) {
	scheme := runtime.NewScheme()
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register product transport: %w", err)
	}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		simulationv1alpha1.GroupVersion,
	})
	resource := simulationv1alpha1.GroupVersion.WithResource("scenarioinstances")
	singular := simulationv1alpha1.GroupVersion.WithResource("scenarioinstance")
	mapper.AddSpecific(
		simulationv1alpha1.GroupVersion.WithKind("ScenarioInstance"),
		resource,
		singular,
		meta.RESTScopeRoot,
	)
	mapper.AddSpecific(
		simulationv1alpha1.GroupVersion.WithKind("ScenarioInstanceList"),
		resource,
		singular,
		meta.RESTScopeRoot,
	)
	kubernetesClient, err := client.NewWithWatch(
		rest.CopyConfig(config),
		client.Options{Scheme: scheme, Mapper: mapper},
	)
	if err != nil {
		return nil, fmt.Errorf("construct Scenario Control Plane client: %w", err)
	}
	return kubernetesClient, nil
}

func digestDomainValue(domainName, value string) string {
	sum := sha256.Sum256([]byte(domainName + "\x00" + value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func parseDigest(value string) (domain.Digest, error) {
	digest, err := domain.ParseDigest(value)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("construct target digest: %w", err)
	}
	return digest, nil
}
