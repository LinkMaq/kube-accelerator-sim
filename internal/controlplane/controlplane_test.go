package controlplane_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	controlplanekubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/memory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestInMemoryScenarioControlPlaneContract(t *testing.T) {
	t.Parallel()

	runSubmissionContract(t, memory.New(memory.Options{HistoryLimit: 8}))
}

func TestKubernetesScenarioControlPlaneContract(t *testing.T) {
	t.Parallel()

	adapter, _ := newKubernetesAdapter(t)
	runSubmissionContract(t, adapter)
}

func TestKubernetesReadBoundsStatusAndRetainsAcceptedRevision(t *testing.T) {
	t.Parallel()

	adapter, kubernetesClient := newKubernetesAdapter(t)
	fixture := newFixture(t)
	created, err := adapter.Submit(context.Background(), fixture.createCommand())
	if err != nil {
		t.Fatal(err)
	}
	instance := &simulationv1alpha1.ScenarioInstance{}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: fixture.name.String()},
		instance,
	); err != nil {
		t.Fatal(err)
	}
	instance.Status.ObservedGeneration = 0
	instance.Status.RevisionDigest = fixture.firstDigest.String()
	instance.Status.Phase = "Failed"
	for index := 0; index < 40; index++ {
		instance.Status.Diagnostics = append(
			instance.Status.Diagnostics,
			simulationv1alpha1.DiagnosticStatus{
				Code:         "ConvergenceFailed",
				Message:      "retained failure",
				ExitCategory: 5,
			},
		)
	}
	for index := 0; index < 70; index++ {
		instance.Status.Inventory = append(
			instance.Status.Inventory,
			simulationv1alpha1.InventoryEntry{
				APIVersion: "v1",
				Kind:       "Node",
				Count:      int32(index),
			},
		)
	}
	for index := 0; index < 40; index++ {
		instance.Status.Fidelity = append(
			instance.Status.Fidelity,
			simulationv1alpha1.FidelitySurfaceStatus{
				Surface: fmt.Sprintf("surface-%02d", index),
				State:   "achieved",
			},
		)
	}
	for index := 0; index < 8; index++ {
		instance.Status.Conditions = append(instance.Status.Conditions, metav1.Condition{
			Type:               "Progressing",
			Status:             metav1.ConditionFalse,
			Reason:             "ConvergenceFailed",
			Message:            "retained failure",
			LastTransitionTime: metav1.NewTime(time.Unix(int64(index+1), 0)),
		})
	}
	if err := kubernetesClient.Status().Update(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	record, err := adapter.Read(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if record.InstanceUID != created.InstanceUID ||
		record.Revision.Digest != fixture.firstDigest ||
		len(record.Revisions) != 1 {
		t.Fatalf("accepted revision was lost after failure: %#v", record)
	}
	if record.Status.Phase != "Failed" ||
		len(record.Status.Diagnostics) != controlplane.MaximumStatusDiagnostics ||
		!record.Status.DiagnosticsTruncated ||
		len(record.Status.Inventory) != controlplane.MaximumStatusInventory ||
		!record.Status.InventoryTruncated ||
		len(record.Status.Fidelity) != controlplane.MaximumStatusFidelity ||
		!record.Status.FidelityTruncated ||
		len(record.Status.Conditions) != controlplane.MaximumStatusConditions ||
		!record.Status.ConditionsTruncated {
		t.Fatalf("status was not defensively bounded: %#v", record.Status)
	}
}

func TestKubernetesReadOrdersGenerationKeyedRevisionReceipts(t *testing.T) {
	t.Parallel()

	adapter, kubernetesClient := newKubernetesAdapter(t)
	fixture := newFixture(t)
	created, err := adapter.Submit(context.Background(), fixture.createCommand())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Submit(
		context.Background(),
		fixture.updateCommand(created, "second"),
	); err != nil {
		t.Fatal(err)
	}
	instance := &simulationv1alpha1.ScenarioInstance{}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: fixture.name.String()},
		instance,
	); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(instance.Spec.Revisions)
	if err := kubernetesClient.Update(context.Background(), instance); err != nil {
		t.Fatal(err)
	}

	record, err := adapter.Read(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if record.Revision.Generation.Value() != 2 ||
		record.Revision.Digest != scenarioDigest("second") ||
		record.Revisions[0].Generation.Value() != 1 ||
		record.Revisions[1].Generation.Value() != 2 {
		t.Fatalf("generation-keyed revisions were not normalized: %#v", record.Revisions)
	}
}

func TestKubernetesWatchResumesAfterOpaqueResourceVersionAndCloses(t *testing.T) {
	t.Parallel()

	adapter, _ := newKubernetesAdapter(t)
	fixture := newFixture(t)
	created, err := adapter.Submit(context.Background(), fixture.createCommand())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Watch(context.Background(), controlplane.WatchCursor{
		Key:                  fixture.key,
		AfterResourceVersion: created.ResourceVersion,
		Limit:                1,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := adapter.Submit(
		context.Background(),
		fixture.updateCommand(created, "watch-update"),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	event, err := stream.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Cursor != updated.ResourceVersion ||
		event.Record.Revision.Digest != scenarioDigest("watch-update") {
		t.Fatalf("unexpected watch event: %#v", event)
	}
	if _, err := stream.Next(ctx); err != io.EOF {
		t.Fatalf("bounded watch closure = %v, want EOF", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKubernetesWatchExpiryReturnsCurrentResumeCursor(t *testing.T) {
	t.Parallel()

	adapter, kubernetesClient := newKubernetesAdapter(t)
	fixture := newFixture(t)
	created, err := adapter.Submit(context.Background(), fixture.createCommand())
	if err != nil {
		t.Fatal(err)
	}
	expiringClient := interceptor.NewClient(
		kubernetesClient,
		interceptor.Funcs{
			Watch: func(
				context.Context,
				client.WithWatch,
				client.ObjectList,
				...client.ListOption,
			) (watch.Interface, error) {
				return nil, apierrors.NewResourceExpired("contract-test")
			},
		},
	)
	expiringAdapter := controlplanekubernetes.New(
		expiringClient,
		"kubernetes-v1alpha1",
	)
	_, err = expiringAdapter.Watch(
		context.Background(),
		controlplane.WatchCursor{
			Key:                  fixture.key,
			AfterResourceVersion: "expired",
			Limit:                1,
		},
	)
	if controlplane.ErrorCodeOf(err) != controlplane.ErrorCursorExpired ||
		controlplane.ResumeCursorOf(err) != created.ResourceVersion {
		t.Fatalf(
			"expired watch error = %v, cursor = %q",
			err,
			controlplane.ResumeCursorOf(err),
		)
	}
}

func TestEnvtestAdmissionStatusFinalizerAndAtomicConflict(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Skip("KUBEBUILDER_ASSETS is required for the pinned envtest lane")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	environment := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDDirectoryPaths: []string{
			filepath.Join(root, "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,
	}
	configuration, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	scheme := runtime.NewScheme()
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubernetesClient, err := client.NewWithWatch(
		configuration,
		client.Options{Scheme: scheme},
	)
	if err != nil {
		t.Fatal(err)
	}
	adapter := controlplanekubernetes.New(kubernetesClient, "kubernetes-v1alpha1")
	fixture := newFixture(t)
	dryRun := fixture.createCommand()
	dryRun.ServerDryRun = true
	proposed, err := adapter.Submit(context.Background(), dryRun)
	if err != nil {
		t.Fatal(err)
	}
	if !proposed.DryRun || proposed.Accepted {
		t.Fatalf("unexpected envtest dry-run receipt: %#v", proposed)
	}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: fixture.name.String()},
		&simulationv1alpha1.ScenarioInstance{},
	); !apierrors.IsNotFound(err) {
		t.Fatalf("server dry-run create error = %v, want NotFound", err)
	}
	created, err := adapter.Submit(context.Background(), fixture.createCommand())
	if err != nil {
		t.Fatal(err)
	}
	instance := &simulationv1alpha1.ScenarioInstance{}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: fixture.name.String()},
		instance,
	); err != nil {
		t.Fatal(err)
	}
	if instance.UID == "" ||
		instance.ResourceVersion == "" ||
		!slices.Contains(instance.Finalizers, "simulation.kasim.io/owned-resources") {
		t.Fatalf("API server identity/finalizer missing: %#v", instance.ObjectMeta)
	}

	stale := instance.DeepCopy()
	next := fixture.updateCommand(created, "envtest-update")
	if _, err := adapter.Submit(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	stale.Labels = map[string]string{"stale": "write"}
	if err := kubernetesClient.Update(context.Background(), stale); !apierrors.IsConflict(err) {
		t.Fatalf("stale resourceVersion update error = %v, want Conflict", err)
	}

	current := &simulationv1alpha1.ScenarioInstance{}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: fixture.name.String()},
		current,
	); err != nil {
		t.Fatal(err)
	}
	current.Spec.Fidelity = "dra-control-plane"
	if err := kubernetesClient.Update(context.Background(), current); err == nil {
		t.Fatal("CRD admission allowed immutable Fidelity Mode mutation")
	}

	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: fixture.name.String()},
		current,
	); err != nil {
		t.Fatal(err)
	}
	current.Status.Phase = "Failed"
	current.Status.ObservedGeneration = 0
	current.Status.RevisionDigest = scenarioDigest("envtest-update").String()
	if err := kubernetesClient.Status().Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.Read(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status.Phase != "Failed" ||
		observed.Revision.Digest != scenarioDigest("envtest-update") ||
		len(observed.Revisions) != 2 {
		t.Fatalf("status update lost accepted receipt: %#v", observed)
	}
}

func newKubernetesAdapter(
	t *testing.T,
) (*controlplanekubernetes.Adapter, client.WithWatch) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var nextUID atomic.Uint64
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&simulationv1alpha1.ScenarioInstance{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(
				ctx context.Context,
				delegate client.WithWatch,
				object client.Object,
				options ...client.CreateOption,
			) error {
				if object.GetUID() == "" {
					object.SetUID(types.UID(
						"fake-" + generationString(nextUID.Add(1)),
					))
				}
				applied := &client.CreateOptions{}
				for _, option := range options {
					option.ApplyToCreate(applied)
				}
				if slices.Contains(applied.DryRun, metav1.DryRunAll) {
					return nil
				}
				return delegate.Create(ctx, object, options...)
			},
			Update: func(
				ctx context.Context,
				delegate client.WithWatch,
				object client.Object,
				options ...client.UpdateOption,
			) error {
				applied := &client.UpdateOptions{}
				for _, option := range options {
					option.ApplyToUpdate(applied)
				}
				if slices.Contains(applied.DryRun, metav1.DryRunAll) {
					return nil
				}
				return delegate.Update(ctx, object, options...)
			},
		}).
		Build()
	return controlplanekubernetes.New(kubernetesClient, "kubernetes-v1alpha1"), kubernetesClient
}

func TestInMemoryWatchIsBoundedResumableAndExpiresOldCursors(t *testing.T) {
	t.Parallel()

	adapter := memory.New(memory.Options{HistoryLimit: 1})
	fixture := newFixture(t)
	first, err := adapter.Submit(context.Background(), fixture.createCommand())
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.Submit(
		context.Background(),
		fixture.updateCommand(first, "second"),
	)
	if err != nil {
		t.Fatal(err)
	}
	third, err := adapter.Submit(
		context.Background(),
		fixture.updateCommand(second, "third"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = adapter.Watch(context.Background(), controlplane.WatchCursor{
		Key:                  fixture.key,
		AfterResourceVersion: first.ResourceVersion,
		Limit:                1,
	})
	if controlplane.ErrorCodeOf(err) != controlplane.ErrorCursorExpired {
		t.Fatalf("Watch() error = %v, want CursorExpired", err)
	}
	resume := controlplane.ResumeCursorOf(err)
	if resume != second.ResourceVersion {
		t.Fatalf("resume cursor = %q, want %q", resume, second.ResourceVersion)
	}

	stream, err := adapter.Watch(context.Background(), controlplane.WatchCursor{
		Key:                  fixture.key,
		AfterResourceVersion: second.ResourceVersion,
		Limit:                1,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Record.Revision.Digest != scenarioDigest("third") ||
		event.Cursor != third.ResourceVersion {
		t.Fatalf("unexpected resumed event: %#v", event)
	}
	if _, err := stream.Next(context.Background()); err != io.EOF {
		t.Fatalf("bounded stream closure = %v, want EOF", err)
	}
}

func runSubmissionContract(t *testing.T, adapter controlplane.ScenarioControlPlane) {
	t.Helper()
	fixture := newFixture(t)
	ctx := context.Background()

	capabilities, err := adapter.Probe(ctx, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.TargetFingerprint != fixture.target.Fingerprint {
		t.Fatal("Probe() lost target identity")
	}

	dryRunCreate := fixture.createCommand()
	dryRunCreate.ServerDryRun = true
	proposed, err := adapter.Submit(ctx, dryRunCreate)
	if err != nil {
		t.Fatal(err)
	}
	if !proposed.DryRun || proposed.Accepted || proposed.NoOp ||
		proposed.DesiredGeneration.Value() != 1 {
		t.Fatalf("unexpected dry-run create receipt: %#v", proposed)
	}
	if _, err := adapter.Read(ctx, fixture.key); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorNotFound {
		t.Fatalf("dry-run create persisted an instance: %v", err)
	}

	created, err := adapter.Submit(ctx, fixture.createCommand())
	if err != nil {
		t.Fatal(err)
	}
	if !created.Accepted || created.NoOp ||
		created.InstanceUID.String() == "" ||
		created.DesiredGeneration.Value() != 1 ||
		created.ResourceVersion == "" {
		t.Fatalf("unexpected create receipt: %#v", created)
	}
	record, err := adapter.Read(ctx, fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if record.InstanceUID != created.InstanceUID ||
		record.Revision.Digest != fixture.firstDigest ||
		record.DesiredGeneration != created.DesiredGeneration {
		t.Fatalf("unexpected created record: %#v", record)
	}

	noOpCommand := fixture.createCommand()
	noOp, err := adapter.Submit(ctx, noOpCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !noOp.NoOp || noOp.Accepted ||
		noOp.DesiredGeneration != created.DesiredGeneration ||
		noOp.ResourceVersion != created.ResourceVersion {
		t.Fatalf("unexpected no-op receipt: %#v", noOp)
	}

	dryRunUpdate := fixture.updateCommand(created, "dry-run-update")
	dryRunUpdate.ServerDryRun = true
	proposed, err = adapter.Submit(ctx, dryRunUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if !proposed.DryRun || proposed.Accepted || proposed.NoOp ||
		proposed.DesiredGeneration.Value() != 2 {
		t.Fatalf("unexpected dry-run update receipt: %#v", proposed)
	}
	afterDryRun, err := adapter.Read(ctx, fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if afterDryRun.DesiredGeneration.Value() != 1 ||
		afterDryRun.Revision.Digest != fixture.firstDigest {
		t.Fatalf("dry-run update persisted desired state: %#v", afterDryRun)
	}

	nextDigest := scenarioDigest("second")
	withoutPreconditions := fixture.createCommand()
	withoutPreconditions.Revision.Digest = nextDigest
	withoutPreconditions.Revision.CanonicalScenario = canonicalScenario("second")
	if _, err := adapter.Submit(ctx, withoutPreconditions); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorUIDConflict {
		t.Fatalf("unguarded update error = %v, want UIDConflict", err)
	}

	wrongGeneration := fixture.updateCommand(created, "second")
	wrongGeneration.Preconditions.ExpectedGeneration = generation(t, 99)
	if _, err := adapter.Submit(ctx, wrongGeneration); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorGenerationConflict {
		t.Fatalf("stale generation error = %v", err)
	}

	wrongResourceVersion := fixture.updateCommand(created, "second")
	wrongResourceVersion.Preconditions.ResourceVersion = "stale"
	if _, err := adapter.Submit(ctx, wrongResourceVersion); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorResourceVersionConflict {
		t.Fatalf("stale resourceVersion error = %v", err)
	}

	wrongFidelity := fixture.updateCommand(created, "second")
	wrongFidelity.Fidelity = fidelity(t, "dra-control-plane")
	if _, err := adapter.Submit(ctx, wrongFidelity); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorFidelityConflict {
		t.Fatalf("fidelity mutation error = %v", err)
	}

	wrongCreationIdentity := fixture.updateCommand(created, "second")
	wrongCreationIdentity.CreationIdentity = "another-user"
	if _, err := adapter.Submit(ctx, wrongCreationIdentity); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorCreationIdentityConflict {
		t.Fatalf("creation identity mutation error = %v", err)
	}

	wrongTarget := fixture.updateCommand(created, "second")
	wrongTarget.Target.Fingerprint = digestOf("another-target")
	if _, err := adapter.Submit(ctx, wrongTarget); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorTargetMismatch {
		t.Fatalf("target mutation error = %v", err)
	}

	updated, err := adapter.Submit(ctx, fixture.updateCommand(created, "second"))
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Accepted || updated.NoOp ||
		updated.DesiredGeneration.Value() != 2 ||
		updated.ResourceVersion == created.ResourceVersion {
		t.Fatalf("unexpected update receipt: %#v", updated)
	}
	updatedRecord, err := adapter.Read(ctx, fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedRecord.Revisions) != 2 ||
		updatedRecord.Revisions[0].Digest != fixture.firstDigest ||
		updatedRecord.Revisions[1].Digest != nextDigest {
		t.Fatalf("logical revisions are not immutable append-only: %#v", updatedRecord.Revisions)
	}

	wrongInstanceUID, err := domain.ParseInstanceUID("wrong-instance-uid")
	if err != nil {
		t.Fatal(err)
	}
	wrongDeleteUID := controlplane.DeletionCommand{
		Target: fixture.target,
		Name:   fixture.name,
		Preconditions: controlplane.DeletionPreconditions{
			InstanceUID:        wrongInstanceUID,
			ExpectedGeneration: updated.DesiredGeneration,
		},
	}
	if _, err := adapter.Delete(
		ctx,
		wrongDeleteUID,
	); controlplane.ErrorCodeOf(err) != controlplane.ErrorUIDConflict {
		t.Fatalf("delete UID error = %v", err)
	}
	wrongDeleteGeneration := wrongDeleteUID
	wrongDeleteGeneration.Preconditions.InstanceUID = updated.InstanceUID
	wrongDeleteGeneration.Preconditions.ExpectedGeneration = generation(t, 99)
	if _, err := adapter.Delete(
		ctx,
		wrongDeleteGeneration,
	); controlplane.ErrorCodeOf(err) != controlplane.ErrorGenerationConflict {
		t.Fatalf("delete generation error = %v", err)
	}
	deletion, err := adapter.Delete(ctx, controlplane.DeletionCommand{
		Target: fixture.target,
		Name:   fixture.name,
		Preconditions: controlplane.DeletionPreconditions{
			InstanceUID:        updated.InstanceUID,
			ExpectedGeneration: updated.DesiredGeneration,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deletion.Accepted ||
		deletion.NoOp ||
		deletion.InstanceUID != updated.InstanceUID ||
		deletion.DesiredGeneration != updated.DesiredGeneration {
		t.Fatalf("unexpected deletion receipt: %#v", deletion)
	}
	deleting, err := adapter.Read(ctx, fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !deleting.DeletionRequested {
		t.Fatal("accepted deletion did not become durable desired state")
	}
	retry, err := adapter.Delete(ctx, controlplane.DeletionCommand{
		Target: fixture.target,
		Name:   fixture.name,
		Preconditions: controlplane.DeletionPreconditions{
			InstanceUID:        updated.InstanceUID,
			ExpectedGeneration: updated.DesiredGeneration,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Accepted || !retry.NoOp {
		t.Fatalf("deletion retry was not idempotent: %#v", retry)
	}
}

type fixture struct {
	target      controlplane.ExplicitTarget
	key         controlplane.InstanceKey
	name        domain.Name
	firstDigest domain.Digest
	fidelity    domain.FidelityMode
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	name, err := domain.ParseName("training-lab")
	if err != nil {
		t.Fatal(err)
	}
	targetDigest, err := domain.ParseDigest("sha256:" + strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	target := controlplane.ExplicitTarget{
		ContextName: "test-context",
		Fingerprint: targetDigest,
	}
	return fixture{
		target: target,
		key: controlplane.InstanceKey{
			TargetFingerprint: targetDigest,
			Name:              name,
		},
		name:        name,
		firstDigest: scenarioDigest("first"),
		fidelity:    fidelity(t, "scheduling"),
	}
}

func (fixture fixture) createCommand() controlplane.RevisionCommand {
	return controlplane.RevisionCommand{
		Target:           fixture.target,
		Name:             fixture.name,
		CreationIdentity: "contract-test",
		Fidelity:         fixture.fidelity,
		Preconditions: controlplane.Preconditions{
			ExpectedGeneration: generationValue(0),
		},
		Revision: controlplane.ScenarioRevision{
			Generation:        generationValue(1),
			Digest:            fixture.firstDigest,
			CanonicalScenario: canonicalScenario("first"),
			Profiles: []controlplane.ProfileReceipt{{
				ID:       "nvidia",
				Revision: "2026-07-30",
				Digest:   digestOf("profile"),
				Class:    "verified",
			}},
		},
	}
}

func (fixture fixture) updateCommand(
	receipt controlplane.SubmissionReceipt,
	seed string,
) controlplane.RevisionCommand {
	return controlplane.RevisionCommand{
		Target:           fixture.target,
		Name:             fixture.name,
		CreationIdentity: "contract-test",
		Fidelity:         fixture.fidelity,
		Preconditions: controlplane.Preconditions{
			InstanceUID:        receipt.InstanceUID,
			ExpectedGeneration: receipt.DesiredGeneration,
			ResourceVersion:    receipt.ResourceVersion,
		},
		Revision: controlplane.ScenarioRevision{
			Generation:        generationValue(int64(receipt.DesiredGeneration.Value() + 1)),
			Digest:            scenarioDigest(seed),
			CanonicalScenario: canonicalScenario(seed),
			Profiles: []controlplane.ProfileReceipt{{
				ID:       "nvidia",
				Revision: "2026-07-30",
				Digest:   digestOf("profile"),
				Class:    "verified",
			}},
		},
	}
}

func digestOf(seed string) domain.Digest {
	sum := sha256.Sum256([]byte(seed))
	digest, err := domain.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
	if err != nil {
		panic(err)
	}
	return digest
}

func canonicalScenario(seed string) []byte {
	return []byte(fmt.Sprintf(`{"name":%q}`, seed))
}

func scenarioDigest(seed string) domain.Digest {
	sum := sha256.Sum256(canonicalScenario(seed))
	digest, err := domain.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
	if err != nil {
		panic(err)
	}
	return digest
}

func fidelity(t *testing.T, value string) domain.FidelityMode {
	t.Helper()
	mode, err := domain.ParseFidelityMode(value)
	if err != nil {
		t.Fatal(err)
	}
	return mode
}

func generation(t *testing.T, value int64) domain.Generation {
	t.Helper()
	result, err := domain.NewGeneration(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func generationValue(value int64) domain.Generation {
	result, err := domain.NewGeneration(value)
	if err != nil {
		panic(err)
	}
	return result
}

func generationString(value uint64) string {
	return fmt.Sprintf("%d", value)
}
