// Package kwok contains the single concrete Synthetic Node runtime shipped by
// the product. Its selectors, assets, and recovery expectations stay behind
// this internal implementation and never enter Scenario or projection types.
package kwok

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"slices"
)

const (
	nodeAnnotationKey           = "kwok.x-k8s.io/node"
	nodeAnnotationValue         = "fake"
	nodeInactiveAnnotationValue = "disabled"
	annotationSelector          = nodeAnnotationKey + "=" + nodeAnnotationValue
	leaseDuration               = int32(40)
)

var requiredStages = []string{
	"node-heartbeat-with-lease",
	"node-initialize",
	"pod-complete",
	"pod-delete",
	"pod-ready",
}

//go:embed stage-fast.yaml
var assets embed.FS

// Runtime is the concrete pinned KWOK implementation. It is deliberately not
// hidden behind a SyntheticRuntime or backend registry.
type Runtime struct{}

// ReleaseLock records every moving upstream identity used by the runtime.
// Downloads are accepted only after their bytes match these digests.
type ReleaseLock struct {
	Version        string
	SourceCommit   string
	ManifestURL    string
	ManifestSHA256 string
	StageURL       string
	StageSHA256    string
	Image          string
}

func Pinned() Runtime {
	return Runtime{}
}

func (Runtime) Lock() ReleaseLock {
	return ReleaseLock{
		Version:        "v0.8.0",
		SourceCommit:   "156033d7df7ea0e09cea82b715fe566ea68aeeb4",
		ManifestURL:    "https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/kwok.yaml",
		ManifestSHA256: "a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405",
		StageURL:       "https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/stage-fast.yaml",
		StageSHA256:    "2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24",
		Image:          "registry.k8s.io/kwok/kwok@sha256:6d25aa8fbdfe78845423160bf125b5513f9522e2770981f0945c2a250c2b26f0",
	}
}

// EmbeddedStages returns a copy of the exact upstream fast Stage asset.
func (Runtime) EmbeddedStages() ([]byte, error) {
	encoded, err := assets.ReadFile("stage-fast.yaml")
	if err != nil {
		return nil, fmt.Errorf("read embedded KWOK Stages: %w", err)
	}
	return append([]byte(nil), encoded...), nil
}

// VerifyEmbeddedAssets proves the vendored Stage bytes still match the lock.
func (runtime Runtime) VerifyEmbeddedAssets() error {
	encoded, err := runtime.EmbeddedStages()
	if err != nil {
		return err
	}
	return VerifyAsset("stage-fast.yaml", encoded, runtime.Lock().StageSHA256)
}

// VerifyAsset validates downloaded or embedded release input before use.
func VerifyAsset(name string, encoded []byte, expectedSHA256 string) error {
	if name == "" || len(encoded) == 0 || len(expectedSHA256) != 64 {
		return fmt.Errorf("KWOK asset verification requires name, bytes, and SHA-256")
	}
	sum := sha256.Sum256(encoded)
	actual := hex.EncodeToString(sum[:])
	if actual != expectedSHA256 {
		return fmt.Errorf(
			"KWOK asset %q SHA-256 = %s, want %s",
			name,
			actual,
			expectedSHA256,
		)
	}
	return nil
}

// InstallationObservation is the exact installer-owned state needed for
// runtime compatibility. It is not exposed in product lifecycle receipts.
type InstallationObservation struct {
	Version            string
	ControllerImage    string
	ManifestSHA256     string
	StageSHA256        string
	AnnotationSelector string
	StageNames         []string
	ControllerReady    bool
}

// CapabilityReport fails closed on any drift from the release lock.
type CapabilityReport struct {
	compatible bool
	issues     []string
}

func (runtime Runtime) Check(observed InstallationObservation) CapabilityReport {
	lock := runtime.Lock()
	issues := make([]string, 0, 7)
	if observed.Version != lock.Version {
		issues = append(issues, fmt.Sprintf(
			"KWOK version %q does not match pinned %q",
			observed.Version,
			lock.Version,
		))
	}
	if observed.ControllerImage != lock.Image {
		issues = append(issues, "KWOK controller image digest does not match the release lock")
	}
	if observed.ManifestSHA256 != lock.ManifestSHA256 {
		issues = append(issues, "KWOK manifest digest does not match the release lock")
	}
	if observed.StageSHA256 != lock.StageSHA256 {
		issues = append(issues, "KWOK Stage digest does not match the release lock")
	}
	if observed.AnnotationSelector != annotationSelector {
		issues = append(issues, "KWOK Node annotation selector does not match the release lock")
	}
	stageSet := make(map[string]struct{}, len(observed.StageNames))
	for _, name := range observed.StageNames {
		stageSet[name] = struct{}{}
	}
	missingStages := make([]string, 0)
	for _, name := range requiredStages {
		if _, found := stageSet[name]; !found {
			missingStages = append(missingStages, name)
		}
	}
	slices.Sort(missingStages)
	if len(missingStages) != 0 {
		issues = append(issues, fmt.Sprintf(
			"KWOK installation is missing required Stages: %v",
			missingStages,
		))
	}
	if !observed.ControllerReady {
		issues = append(issues, "KWOK controller is not Ready")
	}
	return CapabilityReport{
		compatible: len(issues) == 0,
		issues:     issues,
	}
}

func (report CapabilityReport) Compatible() bool {
	return report.compatible
}

func (report CapabilityReport) Issues() []string {
	return append([]string(nil), report.issues...)
}

// NodeContribution is the private concrete metadata and runtime expectation
// merged by the reconciler after projection rendering.
type NodeContribution struct {
	annotations          map[string]string
	inactiveAnnotations  map[string]string
	leaseDurationSeconds int32
	requiresReady        bool
	requiresLease        bool
}

func (Runtime) NodeContribution() NodeContribution {
	return NodeContribution{
		annotations: map[string]string{
			nodeAnnotationKey: nodeAnnotationValue,
		},
		inactiveAnnotations: map[string]string{
			nodeAnnotationKey: nodeInactiveAnnotationValue,
		},
		leaseDurationSeconds: leaseDuration,
		requiresReady:        true,
		requiresLease:        true,
	}
}

func (contribution NodeContribution) Annotations() map[string]string {
	return cloneStringMap(contribution.annotations)
}

func (contribution NodeContribution) InactiveAnnotations() map[string]string {
	return cloneStringMap(contribution.inactiveAnnotations)
}

func (contribution NodeContribution) LeaseDurationSeconds() int32 {
	return contribution.leaseDurationSeconds
}

func (contribution NodeContribution) RequiresReady() bool {
	return contribution.requiresReady
}

func (contribution NodeContribution) RequiresLease() bool {
	return contribution.requiresLease
}

// NodeObservation is the minimum runtime-specific state used after a restart.
type NodeObservation struct {
	Annotations map[string]string
	Ready       bool
	Lease       bool
}

// RecoveryDecision resumes metadata and observation gates without allocating
// a new identity or opening scheduling early.
type RecoveryDecision struct {
	ensureMetadata    bool
	awaitReady        bool
	awaitLease        bool
	mayOpenScheduling bool
}

func (Runtime) Recover(observed NodeObservation) RecoveryDecision {
	metadataReady := observed.Annotations[nodeAnnotationKey] == nodeAnnotationValue
	return RecoveryDecision{
		ensureMetadata:    !metadataReady,
		awaitReady:        !observed.Ready,
		awaitLease:        !observed.Lease,
		mayOpenScheduling: metadataReady && observed.Ready && observed.Lease,
	}
}

func (decision RecoveryDecision) EnsureMetadata() bool {
	return decision.ensureMetadata
}

func (decision RecoveryDecision) AwaitReady() bool {
	return decision.awaitReady
}

func (decision RecoveryDecision) AwaitLease() bool {
	return decision.awaitLease
}

func (decision RecoveryDecision) MayOpenScheduling() bool {
	return decision.mayOpenScheduling
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
