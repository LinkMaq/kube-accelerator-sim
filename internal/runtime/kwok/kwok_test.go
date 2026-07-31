package kwok_test

import (
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/runtime/kwok"
)

func TestPinnedRuntimeVerifiesAssetsAndExactInstallation(t *testing.T) {
	t.Parallel()

	runtime := kwok.Pinned()
	lock := runtime.Lock()
	if lock.Version != "v0.8.0" ||
		lock.SourceCommit != "156033d7df7ea0e09cea82b715fe566ea68aeeb4" ||
		lock.ManifestSHA256 != "a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405" ||
		lock.StageSHA256 != "2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24" ||
		lock.Image != "registry.k8s.io/kwok/kwok@sha256:6d25aa8fbdfe78845423160bf125b5513f9522e2770981f0945c2a250c2b26f0" {
		t.Fatalf("unexpected KWOK lock: %#v", lock)
	}
	if err := runtime.VerifyEmbeddedAssets(); err != nil {
		t.Fatal(err)
	}
	stages, err := runtime.EmbeddedStages()
	if err != nil {
		t.Fatal(err)
	}
	corrupted := append(stages, '\n')
	if err := kwok.VerifyAsset("stage-fast.yaml", corrupted, lock.StageSHA256); err == nil {
		t.Fatal("corrupted KWOK Stage asset unexpectedly matched the release lock")
	}

	report := runtime.Check(kwok.InstallationObservation{
		Version:            lock.Version,
		ControllerImage:    lock.Image,
		ManifestSHA256:     lock.ManifestSHA256,
		StageSHA256:        lock.StageSHA256,
		AnnotationSelector: "kwok.x-k8s.io/node=fake",
		StageNames: []string{
			"node-heartbeat-with-lease",
			"node-initialize",
			"pod-complete",
			"pod-delete",
			"pod-ready",
		},
		ControllerReady: true,
	})
	if !report.Compatible() || len(report.Issues()) != 0 {
		t.Fatalf("exact pinned installation = compatible %t, issues %v",
			report.Compatible(), report.Issues())
	}

	mismatched := runtime.Check(kwok.InstallationObservation{
		Version:            "v0.8.1",
		ControllerImage:    lock.Image,
		ManifestSHA256:     lock.ManifestSHA256,
		StageSHA256:        lock.StageSHA256,
		AnnotationSelector: "kwok.x-k8s.io/node=fake",
		ControllerReady:    true,
	})
	if mismatched.Compatible() ||
		!strings.Contains(strings.Join(mismatched.Issues(), "\n"), "version") {
		t.Fatalf("mismatched installation was not rejected: %v", mismatched.Issues())
	}
}

func TestPinnedRuntimeContributesPrivateLifecycleMetadataAndRecovery(t *testing.T) {
	t.Parallel()

	runtime := kwok.Pinned()
	contribution := runtime.NodeContribution()
	if contribution.Annotations()["kwok.x-k8s.io/node"] != "fake" {
		t.Fatalf("unexpected KWOK annotation: %v", contribution.Annotations())
	}
	if contribution.InactiveAnnotations()["kwok.x-k8s.io/node"] != "disabled" {
		t.Fatalf(
			"unexpected inactive KWOK annotation: %v",
			contribution.InactiveAnnotations(),
		)
	}
	if contribution.LeaseDurationSeconds() != 40 ||
		!contribution.RequiresReady() ||
		!contribution.RequiresLease() {
		t.Fatalf("unexpected lifecycle contribution: %#v", contribution)
	}

	waiting := runtime.Recover(kwok.NodeObservation{
		Annotations: map[string]string{"kwok.x-k8s.io/node": "fake"},
		Ready:       true,
		Lease:       false,
	})
	if waiting.EnsureMetadata() || waiting.MayOpenScheduling() || !waiting.AwaitLease() {
		t.Fatalf("unexpected partial recovery decision: %#v", waiting)
	}
	complete := runtime.Recover(kwok.NodeObservation{
		Annotations: map[string]string{"kwok.x-k8s.io/node": "fake"},
		Ready:       true,
		Lease:       true,
	})
	if !complete.MayOpenScheduling() || complete.AwaitReady() || complete.AwaitLease() {
		t.Fatalf("unexpected complete recovery decision: %#v", complete)
	}
}
