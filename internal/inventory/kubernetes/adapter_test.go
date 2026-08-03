package kubernetes

import (
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory"
)

func TestWatchEventsMaintainLatestBoundedSourceState(t *testing.T) {
	t.Parallel()

	spec := sourceSpec{name: inventory.SourceNodes, gvr: nodesGVR, limit: 2}
	stream := &sourceStream{
		dirty:   make(chan struct{}, 1),
		states:  make(map[inventory.SourceName]inventory.SourceState),
		objects: make(map[inventory.SourceName][]unstructured.Unstructured),
	}
	first := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": "node-a", "uid": "uid-a"},
		"spec":     map[string]any{"unschedulable": false},
	}}
	if err := stream.applyWatchEvent(spec, watch.Added, first); err != nil {
		t.Fatal(err)
	}
	modified := *first.DeepCopy()
	if err := unstructured.SetNestedField(modified.Object, true, "spec", "unschedulable"); err != nil {
		t.Fatal(err)
	}
	if err := stream.applyWatchEvent(spec, watch.Modified, modified); err != nil {
		t.Fatal(err)
	}
	if len(stream.objects[spec.name]) != 1 {
		t.Fatalf("objects after modify = %d", len(stream.objects[spec.name]))
	}
	value, _, _ := unstructured.NestedBool(
		stream.objects[spec.name][0].Object,
		"spec",
		"unschedulable",
	)
	if !value || stream.states[spec.name].Mode != inventory.SourceModeLive {
		t.Fatalf("watch state = %#v object = %#v", stream.states[spec.name], stream.objects[spec.name])
	}
	if err := stream.applyWatchEvent(spec, watch.Deleted, modified); err != nil {
		t.Fatal(err)
	}
	if len(stream.objects[spec.name]) != 0 {
		t.Fatalf("objects after delete = %d", len(stream.objects[spec.name]))
	}
}

func TestSourceErrorsPreserveLastSuccessAndTruthfulAvailability(t *testing.T) {
	t.Parallel()

	spec := sourceSpec{name: inventory.SourcePods, optional: false}
	lastSuccess := time.Now().UTC().Add(-staleAfter - time.Second)
	previous := inventory.SourceState{
		Name: spec.name, Availability: inventory.AvailabilityAvailable,
		Mode: inventory.SourceModeLive, Freshness: inventory.FreshnessFresh,
		LastSuccess: lastSuccess,
	}
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "pods"},
		"",
		fmt.Errorf("secret server detail"),
	)
	state := sourceErrorState(spec, previous, time.Now().UTC(), forbidden)
	if state.Availability != inventory.AvailabilityForbidden ||
		state.Diagnostic != "list permission is forbidden for this source" ||
		!state.LastSuccess.Equal(lastSuccess) {
		t.Fatalf("forbidden state = %#v", state)
	}
	failed := sourceErrorState(spec, previous, time.Now().UTC(), fmt.Errorf("dial secret-host"))
	if failed.Availability != inventory.AvailabilityFailed ||
		failed.Freshness != inventory.FreshnessStale ||
		failed.Diagnostic == "dial secret-host" {
		t.Fatalf("failed state = %#v", failed)
	}

	optional := sourceSpec{name: inventory.SourceResourceSlices, optional: true, gvr: resourceSlicesGVR}
	notFound := apierrors.NewNotFound(
		schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceslices"},
		"",
	)
	state = sourceErrorState(optional, inventory.SourceState{}, time.Now().UTC(), notFound)
	if state.Availability != inventory.AvailabilityUnsupportedSchema {
		t.Fatalf("optional API state = %#v", state)
	}
}

func TestSupportedKubernetesMinorIsStrictlyBounded(t *testing.T) {
	t.Parallel()

	for _, minor := range []string{"30", "34+", "36"} {
		if _, err := supportedMinor(&version.Info{
			Major: "1", Minor: minor, GitVersion: "v1." + minor,
		}); err != nil {
			t.Fatalf("minor %q: %v", minor, err)
		}
	}
	for _, minor := range []string{"29", "37"} {
		if _, err := supportedMinor(&version.Info{
			Major: "1", Minor: minor, GitVersion: "v1." + minor,
		}); err == nil {
			t.Fatalf("minor %q unexpectedly supported", minor)
		}
	}
}

func TestObjectKeyPrefersUIDAndFallsBackToNamespacedName(t *testing.T) {
	t.Parallel()

	object := unstructured.Unstructured{}
	object.SetNamespace("workloads")
	object.SetName("claim")
	if got := objectKey(object); got != "workloads\x00claim" {
		t.Fatalf("fallback key = %q", got)
	}
	object.SetUID(types.UID("stable-uid"))
	if got := objectKey(object); got != "stable-uid" {
		t.Fatalf("UID key = %q", got)
	}
}
