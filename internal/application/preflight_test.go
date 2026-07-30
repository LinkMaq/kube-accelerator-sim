package application_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/application"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster/recording"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/memory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestApplyPreflightRunsInOrderAndMakesZeroPersistentWrites(t *testing.T) {
	t.Parallel()

	trace := []string{}
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	create := revisionCommand(t, 1, "first")
	created, err := controlAdapter.Submit(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	update := revisionCommand(t, 2, "second")
	update.Preconditions = controlplane.Preconditions{
		InstanceUID:        created.InstanceUID,
		ExpectedGeneration: created.DesiredGeneration,
	}
	clusterAdapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion:   "v1.36.3",
			KubernetesMinor: 36,
		},
	})
	connection := application.ConnectedTarget{
		Receipt:      targetReceipt(update.Target),
		Target:       update.Target,
		ControlPlane: &tracedControlPlane{trace: &trace, delegate: controlAdapter},
		Cluster:      &tracedCluster{trace: &trace, delegate: clusterAdapter},
	}
	result, err := application.PreflightApply(
		context.Background(),
		application.PreflightApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: "/explicit/config",
				ContextName:    "test-context",
			},
			Intent: intentOf(update),
			RequiredAccess: []cluster.AccessRequirement{{
				Verb:     "update",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
				Name:     "training-lab",
			}},
		},
		application.ConnectorFunc(func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			trace = append(trace, "connect")
			return connection, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trace, []string{
		"connect",
		"probe",
		"discover",
		"authorize",
		"read",
		"observe",
		"submit-dry-run",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preflight order = %#v, want %#v", got, want)
	}
	if !result.Proposed.DryRun || result.Proposed.Accepted ||
		result.Proposed.DesiredGeneration.Value() != 2 ||
		result.Intent.Preconditions.ResourceVersion != created.ResourceVersion ||
		result.Warning == "" {
		t.Fatalf("unexpected preflight result: %#v", result)
	}
	current, err := controlAdapter.Read(context.Background(), controlplane.InstanceKey{
		TargetFingerprint: update.Target.Fingerprint,
		Name:              update.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.DesiredGeneration.Value() != 1 ||
		len(clusterAdapter.PersistentChangeSets()) != 0 {
		t.Fatalf(
			"preflight persisted state: generation=%d changes=%d",
			current.DesiredGeneration.Value(),
			len(clusterAdapter.PersistentChangeSets()),
		)
	}
}

func TestApplyPreflightStopsOnAuthorizationDenialBeforeReadsOrDryRun(t *testing.T) {
	t.Parallel()

	trace := []string{}
	command := revisionCommand(t, 1, "first")
	requirement := cluster.AccessRequirement{
		Verb:     "create",
		Group:    "simulation.kasim.io",
		Resource: "scenarioinstances",
	}
	controlAdapter := memory.New(memory.Options{})
	clusterAdapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion:   "v1.36.3",
			KubernetesMinor: 36,
		},
		Denied: []cluster.AccessRequirement{requirement},
	})
	connection := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: &tracedControlPlane{trace: &trace, delegate: controlAdapter},
		Cluster:      &tracedCluster{trace: &trace, delegate: clusterAdapter},
	}
	_, err := application.PreflightApply(
		context.Background(),
		application.PreflightApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: "/explicit/config",
				ContextName:    "test-context",
			},
			Intent:         intentOf(command),
			RequiredAccess: []cluster.AccessRequirement{requirement},
		},
		application.ConnectorFunc(func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			trace = append(trace, "connect")
			return connection, nil
		}),
	)
	if cluster.ErrorCodeOf(err) != cluster.ErrorAuthorizationDenied {
		t.Fatalf("authorization error = %v", err)
	}
	if got, want := trace, []string{
		"connect",
		"probe",
		"discover",
		"authorize",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("denied preflight calls = %#v, want %#v", got, want)
	}
	if _, err := controlAdapter.Read(context.Background(), controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}); controlplane.ErrorCodeOf(err) != controlplane.ErrorNotFound {
		t.Fatalf("denied preflight persisted a revision: %v", err)
	}
	if len(clusterAdapter.PersistentChangeSets()) != 0 {
		t.Fatal("denied preflight persisted Cluster changes")
	}
}

func TestApplyPreflightRejectsBeforeConnectionWhenCanonicalDigestIsInvalid(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	command.Revision.Digest = digest(t, "not-the-canonical-bytes")
	connected := false
	_, err := application.PreflightApply(
		context.Background(),
		application.PreflightApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: "/explicit/config",
				ContextName:    "test-context",
			},
			Intent: intentOf(command),
			RequiredAccess: []cluster.AccessRequirement{{
				Verb:     "create",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
			}},
		},
		application.ConnectorFunc(func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			connected = true
			return application.ConnectedTarget{}, errors.New("must not connect")
		}),
	)
	if controlplane.ErrorCodeOf(err) != controlplane.ErrorInvalidCommand {
		t.Fatalf("invalid offline command error = %v", err)
	}
	if connected {
		t.Fatal("invalid offline command contacted Kubernetes")
	}
}

func TestApplyPreflightStopsWhenRuntimeDiscoveryIsUnavailable(t *testing.T) {
	t.Parallel()

	trace := []string{}
	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{})
	clusterAdapter := recording.New(recording.Options{
		Errors: map[recording.Call]error{
			recording.CallDiscover: cluster.NewError(
				cluster.ErrorRuntimeUnavailable,
				"product runtime is not installed",
				false,
			),
		},
	})
	connection := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: &tracedControlPlane{trace: &trace, delegate: controlAdapter},
		Cluster:      &tracedCluster{trace: &trace, delegate: clusterAdapter},
	}
	_, err := application.PreflightApply(
		context.Background(),
		application.PreflightApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: "/explicit/config",
				ContextName:    "test-context",
			},
			Intent: intentOf(command),
			RequiredAccess: []cluster.AccessRequirement{{
				Verb:     "create",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
			}},
		},
		application.ConnectorFunc(func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			trace = append(trace, "connect")
			return connection, nil
		}),
	)
	if cluster.ErrorCodeOf(err) != cluster.ErrorRuntimeUnavailable {
		t.Fatalf("runtime discovery error = %v", err)
	}
	if got, want := trace, []string{"connect", "probe", "discover"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime rejection calls = %#v, want %#v", got, want)
	}
	if _, err := controlAdapter.Read(context.Background(), controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}); controlplane.ErrorCodeOf(err) != controlplane.ErrorNotFound {
		t.Fatalf("runtime rejection persisted a revision: %v", err)
	}
}

func TestApplyPreflightRejectsUnsupportedStableDRABeforeAuthorizationOrAcceptance(
	t *testing.T,
) {
	t.Parallel()

	tests := []struct {
		name         string
		capabilities cluster.TargetCapabilities
		wantCode     cluster.ErrorCode
	}{
		{
			name:         "Kubernetes 1.33",
			capabilities: stableDRACapabilities(33),
			wantCode:     cluster.ErrorKubernetesVersionUnsupported,
		},
		{
			name: "beta only",
			capabilities: cluster.TargetCapabilities{
				ServerVersion:   "v1.34.0",
				KubernetesMinor: 34,
				Resources: []cluster.ResourceCapability{{
					GroupVersion: "resource.k8s.io/v1beta2",
					Resource:     "deviceclasses",
					Verbs:        []string{"get", "list", "watch", "create", "patch", "delete"},
				}},
			},
			wantCode: cluster.ErrorCapabilityUnavailable,
		},
		{
			name: "missing ResourceClaim watch",
			capabilities: mutateResourceCapability(
				stableDRACapabilities(34),
				"resource.k8s.io/v1",
				"resourceclaims",
				func(capability *cluster.ResourceCapability) {
					capability.Verbs = []string{"get", "list"}
				},
			),
			wantCode: cluster.ErrorCapabilityUnavailable,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			trace := []string{}
			command := revisionCommand(t, 1, "first")
			dra, err := domain.ParseFidelityMode("dra-control-plane")
			if err != nil {
				t.Fatal(err)
			}
			command.Fidelity = dra
			controlAdapter := memory.New(memory.Options{})
			clusterAdapter := recording.New(recording.Options{
				Capabilities: test.capabilities,
			})
			connection := application.ConnectedTarget{
				Receipt:      targetReceipt(command.Target),
				Target:       command.Target,
				ControlPlane: &tracedControlPlane{trace: &trace, delegate: controlAdapter},
				Cluster:      &tracedCluster{trace: &trace, delegate: clusterAdapter},
			}
			_, err = application.PreflightApply(
				context.Background(),
				application.PreflightApplyRequest{
					Selection: cluster.TargetSelection{
						KubeconfigPath: "/explicit/config",
						ContextName:    "test-context",
					},
					Intent: intentOf(command),
					RequiredAccess: []cluster.AccessRequirement{{
						Verb:     "create",
						Group:    "simulation.kasim.io",
						Resource: "scenarioinstances",
					}},
				},
				func(
					context.Context,
					cluster.TargetSelection,
				) (application.ConnectedTarget, error) {
					trace = append(trace, "connect")
					return connection, nil
				},
			)
			if cluster.ErrorCodeOf(err) != test.wantCode {
				t.Fatalf("preflight error = %v, want %s", err, test.wantCode)
			}
			if got, want := trace, []string{"connect", "probe", "discover"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("DRA rejection trace = %#v, want %#v", got, want)
			}
			if _, err := controlAdapter.Read(
				context.Background(),
				controlplane.InstanceKey{
					TargetFingerprint: command.Target.Fingerprint,
					Name:              command.Name,
				},
			); controlplane.ErrorCodeOf(err) != controlplane.ErrorNotFound {
				t.Fatalf("unsupported DRA preflight persisted a revision: %v", err)
			}
		})
	}
}

func TestApplyPreflightRejectsMissingDRAReadPermissionBeforeAcceptance(
	t *testing.T,
) {
	t.Parallel()

	trace := []string{}
	command := revisionCommand(t, 1, "first")
	draFidelity, err := domain.ParseFidelityMode("dra-control-plane")
	if err != nil {
		t.Fatal(err)
	}
	command.Fidelity = draFidelity
	denied := cluster.AccessRequirement{
		Verb:          "list",
		Group:         "resource.k8s.io",
		Resource:      "resourceclaims",
		Namespaced:    true,
		AllNamespaces: true,
	}
	controlAdapter := memory.New(memory.Options{})
	clusterAdapter := recording.New(recording.Options{
		Capabilities: stableDRACapabilities(34),
		Denied:       []cluster.AccessRequirement{denied},
	})
	connection := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: &tracedControlPlane{trace: &trace, delegate: controlAdapter},
		Cluster:      &tracedCluster{trace: &trace, delegate: clusterAdapter},
	}
	_, err = application.PreflightApply(
		context.Background(),
		application.PreflightApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: "/explicit/config",
				ContextName:    "test-context",
			},
			Intent: intentOf(command),
			RequiredAccess: []cluster.AccessRequirement{{
				Verb:     "create",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
			}},
		},
		func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			trace = append(trace, "connect")
			return connection, nil
		},
	)
	if cluster.ErrorCodeOf(err) != cluster.ErrorAuthorizationDenied {
		t.Fatalf("DRA authorization error = %v", err)
	}
	if got, want := trace, []string{
		"connect",
		"probe",
		"discover",
		"authorize",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DRA authorization rejection trace = %#v, want %#v", got, want)
	}
	if _, err := controlAdapter.Read(
		context.Background(),
		controlplane.InstanceKey{
			TargetFingerprint: command.Target.Fingerprint,
			Name:              command.Name,
		},
	); controlplane.ErrorCodeOf(err) != controlplane.ErrorNotFound {
		t.Fatalf("DRA permission rejection persisted a revision: %v", err)
	}
}

type tracedControlPlane struct {
	trace    *[]string
	delegate controlplane.ScenarioControlPlane
}

func (adapter *tracedControlPlane) Probe(
	ctx context.Context,
	target controlplane.ExplicitTarget,
) (controlplane.TargetCapabilities, error) {
	*adapter.trace = append(*adapter.trace, "probe")
	return adapter.delegate.Probe(ctx, target)
}

func (adapter *tracedControlPlane) Read(
	ctx context.Context,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	*adapter.trace = append(*adapter.trace, "read")
	return adapter.delegate.Read(ctx, key)
}

func (adapter *tracedControlPlane) Submit(
	ctx context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	if command.ServerDryRun {
		*adapter.trace = append(*adapter.trace, "submit-dry-run")
	} else {
		*adapter.trace = append(*adapter.trace, "submit")
	}
	return adapter.delegate.Submit(ctx, command)
}

func (adapter *tracedControlPlane) Delete(
	ctx context.Context,
	command controlplane.DeletionCommand,
) (controlplane.DeletionReceipt, error) {
	*adapter.trace = append(*adapter.trace, "delete")
	return adapter.delegate.Delete(ctx, command)
}

func (adapter *tracedControlPlane) Watch(
	ctx context.Context,
	cursor controlplane.WatchCursor,
) (controlplane.InstanceEventStream, error) {
	*adapter.trace = append(*adapter.trace, "watch")
	return adapter.delegate.Watch(ctx, cursor)
}

type tracedCluster struct {
	trace    *[]string
	delegate cluster.Port
}

func (adapter *tracedCluster) Discover(
	ctx context.Context,
) (cluster.TargetCapabilities, error) {
	*adapter.trace = append(*adapter.trace, "discover")
	return adapter.delegate.Discover(ctx)
}

func (adapter *tracedCluster) Authorize(
	ctx context.Context,
	requirements []cluster.AccessRequirement,
) (cluster.AuthorizationReport, error) {
	*adapter.trace = append(*adapter.trace, "authorize")
	return adapter.delegate.Authorize(ctx, requirements)
}

func (adapter *tracedCluster) Observe(
	ctx context.Context,
	scope cluster.OwnershipScope,
) (cluster.ObservedGraph, error) {
	*adapter.trace = append(*adapter.trace, "observe")
	return adapter.delegate.Observe(ctx, scope)
}

func (adapter *tracedCluster) Execute(
	ctx context.Context,
	changeSet cluster.OwnedChangeSet,
) (cluster.MutationReceipt, error) {
	return adapter.delegate.Execute(ctx, changeSet)
}

func revisionCommand(
	t *testing.T,
	generationValue int64,
	seed string,
) controlplane.RevisionCommand {
	t.Helper()
	targetDigest, err := domain.ParseDigest(
		"sha256:" + strings.Repeat("1", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	name, err := domain.ParseName("training-lab")
	if err != nil {
		t.Fatal(err)
	}
	fidelity, err := domain.ParseFidelityMode("scheduling")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(generationValue)
	if err != nil {
		t.Fatal(err)
	}
	canonical := []byte(`{"name":"` + seed + `"}`)
	return controlplane.RevisionCommand{
		Target: controlplane.ExplicitTarget{
			ContextName: "test-context",
			Fingerprint: targetDigest,
		},
		Name:             name,
		CreationIdentity: "application-test",
		Fidelity:         fidelity,
		Revision: controlplane.ScenarioRevision{
			Generation:        generation,
			Digest:            digestBytes(t, canonical),
			CanonicalScenario: canonical,
			Profiles: []controlplane.ProfileReceipt{{
				ID:       "nvidia",
				Revision: "2026-07-30",
				Digest:   digest(t, "profile"),
				Class:    "verified",
			}},
		},
	}
}

func intentOf(command controlplane.RevisionCommand) controlplane.RevisionIntent {
	return controlplane.RevisionIntent{
		Name:             command.Name,
		CreationIdentity: command.CreationIdentity,
		Fidelity:         command.Fidelity,
		Preconditions:    command.Preconditions,
		Revision:         controlplane.CloneRevision(command.Revision),
	}
}

func targetReceipt(
	target controlplane.ExplicitTarget,
) cluster.ConnectionReceipt {
	return cluster.ConnectionReceipt{
		ContextName:             target.ContextName,
		CanonicalKubeconfigPath: "/explicit/config",
		APIServerURL:            "https://example.invalid",
		TargetFingerprint:       target.Fingerprint,
		CADigest:                target.Fingerprint,
	}
}

func stableDRACapabilities(minor int) cluster.TargetCapabilities {
	mutateVerbs := []string{"get", "list", "watch", "create", "patch", "delete"}
	return cluster.TargetCapabilities{
		ServerVersion:   "v1.34.0",
		KubernetesMinor: minor,
		Resources: []cluster.ResourceCapability{
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "deviceclasses",
				Verbs:        mutateVerbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "resourceslices",
				Verbs:        mutateVerbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "resourceclaims",
				Namespaced:   true,
				Verbs:        []string{"get", "list", "watch"},
			},
			{
				GroupVersion: "v1",
				Resource:     "pods",
				Namespaced:   true,
				Verbs:        []string{"get", "list", "watch"},
			},
		},
	}
}

func mutateResourceCapability(
	capabilities cluster.TargetCapabilities,
	groupVersion,
	resource string,
	mutate func(*cluster.ResourceCapability),
) cluster.TargetCapabilities {
	result := capabilities
	result.Resources = append([]cluster.ResourceCapability(nil), capabilities.Resources...)
	for index := range result.Resources {
		result.Resources[index].Verbs = append(
			[]string(nil),
			result.Resources[index].Verbs...,
		)
		if result.Resources[index].GroupVersion == groupVersion &&
			result.Resources[index].Resource == resource {
			mutate(&result.Resources[index])
		}
	}
	return result
}

func digest(t *testing.T, seed string) domain.Digest {
	t.Helper()
	return digestBytes(t, []byte(seed))
}

func digestBytes(t *testing.T, value []byte) domain.Digest {
	t.Helper()
	sum := sha256.Sum256(value)
	result, err := domain.ParseDigest(
		"sha256:" + hex.EncodeToString(sum[:]),
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
