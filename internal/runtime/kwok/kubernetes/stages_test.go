package kubernetes_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	kwokkubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/runtime/kwok/kubernetes"
)

var stageResource = schema.GroupVersionResource{
	Group:    "kwok.x-k8s.io",
	Version:  "v1alpha1",
	Resource: "stages",
}

func TestApplyCreatesExactlyOwnedPinnedStages(t *testing.T) {
	t.Parallel()

	client := newClient()
	if err := kwokkubernetes.ApplyStages(context.Background(), client); err != nil {
		t.Fatalf("apply stages: %v", err)
	}
	list, err := client.Resource(stageResource).List(
		context.Background(),
		metav1.ListOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 5 {
		t.Fatalf("created %d Stages, want 5", len(list.Items))
	}
	for _, item := range list.Items {
		if got := item.GetAnnotations()["simulation.kasim.io/ownership-root"]; got !=
			"kasim-runtime/v1alpha1" {
			t.Errorf("%s ownership root = %q", item.GetName(), got)
		}
		if got := item.GetAnnotations()["simulation.kasim.io/kwok-stage-sha256"]; got !=
			"2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24" {
			t.Errorf("%s Stage digest = %q", item.GetName(), got)
		}
	}
}

func TestApplyRejectsIncompatibleStageBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	existing := stage("node-initialize", "someone-else/v1")
	client := newClient(existing)
	if err := kwokkubernetes.ApplyStages(context.Background(), client); err == nil {
		t.Fatal("apply unexpectedly adopted an incompatible Stage")
	}
	list, err := client.Resource(stageResource).List(
		context.Background(),
		metav1.ListOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("failed preflight left %d Stages, want original 1", len(list.Items))
	}
}

func TestDeleteRemovesOnlyExactCompatibleStages(t *testing.T) {
	t.Parallel()

	client := newClient()
	if err := kwokkubernetes.ApplyStages(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	userOwned := stage("user-owned", "")
	if _, err := client.Resource(stageResource).Create(
		context.Background(),
		userOwned,
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}
	if err := kwokkubernetes.DeleteStages(context.Background(), client); err != nil {
		t.Fatalf("delete stages: %v", err)
	}
	list, err := client.Resource(stageResource).List(
		context.Background(),
		metav1.ListOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].GetName() != "user-owned" {
		t.Fatalf("delete left unexpected Stages: %#v", list.Items)
	}
}

func stage(name, ownershipRoot string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kwok.x-k8s.io/v1alpha1",
		"kind":       "Stage",
		"metadata": map[string]any{
			"name": name,
		},
		"spec": map[string]any{
			"resourceRef": map[string]any{
				"apiGroup": "v1",
				"kind":     "Pod",
			},
		},
	}}
	if ownershipRoot != "" {
		object.SetAnnotations(map[string]string{
			"simulation.kasim.io/ownership-root": ownershipRoot,
		})
	}
	return object
}

func newClient(
	objects ...runtime.Object,
) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			stageResource: "StageList",
		},
		objects...,
	)
}
