// Package kubernetes installs and removes the five pinned KWOK Stage objects
// under one explicit chart ownership root. It is used only by Helm hooks.
package kubernetes

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/LinkMaq/kube-accelerator-sim/internal/runtime/kwok"
)

const (
	ownershipAnnotation = "simulation.kasim.io/ownership-root"
	ownershipRoot       = "kasim-runtime/v1alpha1"
	stageDigestKey      = "simulation.kasim.io/kwok-stage-sha256"
	managedByLabel      = "app.kubernetes.io/managed-by"
	managedByValue      = "kube-accelerator-sim"
)

var (
	stageResource = schema.GroupVersionResource{
		Group:    "kwok.x-k8s.io",
		Version:  "v1alpha1",
		Resource: "stages",
	}
	stageNames = []string{
		"node-heartbeat-with-lease",
		"node-initialize",
		"pod-complete",
		"pod-delete",
		"pod-ready",
	}
)

// ApplyStages fails preflight before the first write when any exact Stage name
// is owned by an incompatible installation.
func ApplyStages(ctx context.Context, client dynamic.Interface) error {
	if client == nil {
		return fmt.Errorf("KWOK Stage installer requires a dynamic client")
	}
	desired, err := desiredStages()
	if err != nil {
		return err
	}
	resource := client.Resource(stageResource)
	existing := make(map[string]*unstructured.Unstructured, len(desired))
	for _, stage := range desired {
		current, err := resource.Get(
			ctx,
			stage.GetName(),
			metav1.GetOptions{},
		)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("preflight KWOK Stage %q: %w", stage.GetName(), err)
		}
		if err := requireCompatible(current); err != nil {
			return err
		}
		existing[stage.GetName()] = current
	}

	for _, stage := range desired {
		current := existing[stage.GetName()]
		if current == nil {
			if _, err := resource.Create(
				ctx,
				stage,
				metav1.CreateOptions{},
			); err != nil {
				return fmt.Errorf("create KWOK Stage %q: %w", stage.GetName(), err)
			}
			continue
		}
		stage.SetResourceVersion(current.GetResourceVersion())
		stage.SetUID(current.GetUID())
		if _, err := resource.Update(
			ctx,
			stage,
			metav1.UpdateOptions{},
		); err != nil {
			return fmt.Errorf("update KWOK Stage %q: %w", stage.GetName(), err)
		}
	}
	return nil
}

// DeleteStages preflights all five exact names before deleting any, then uses
// UID preconditions. Unrelated Stage objects are never listed or selected.
func DeleteStages(ctx context.Context, client dynamic.Interface) error {
	if client == nil {
		return fmt.Errorf("KWOK Stage installer requires a dynamic client")
	}
	resource := client.Resource(stageResource)
	existing := make([]*unstructured.Unstructured, 0, len(stageNames))
	for _, name := range stageNames {
		current, err := resource.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("preflight KWOK Stage %q deletion: %w", name, err)
		}
		if err := requireCompatible(current); err != nil {
			return err
		}
		existing = append(existing, current)
	}
	for _, current := range existing {
		uid := current.GetUID()
		options := metav1.DeleteOptions{}
		if uid != "" {
			options.Preconditions = &metav1.Preconditions{UID: &uid}
		}
		if err := resource.Delete(ctx, current.GetName(), options); err != nil &&
			!apierrors.IsNotFound(err) {
			return fmt.Errorf(
				"delete KWOK Stage %q with UID precondition: %w",
				current.GetName(),
				err,
			)
		}
	}
	return nil
}

func desiredStages() ([]*unstructured.Unstructured, error) {
	runtime := kwok.Pinned()
	if err := runtime.VerifyEmbeddedAssets(); err != nil {
		return nil, err
	}
	lock := runtime.Lock()
	encoded, err := runtime.EmbeddedStages()
	if err != nil {
		return nil, err
	}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(encoded)))
	stages := make([]*unstructured.Unstructured, 0, len(stageNames))
	for {
		document, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read embedded KWOK Stage document: %w", err)
		}
		if len(bytes.TrimSpace(document)) == 0 {
			continue
		}
		object := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(document, &object.Object); err != nil {
			return nil, fmt.Errorf("decode embedded KWOK Stage: %w", err)
		}
		if object.GetAPIVersion() != "kwok.x-k8s.io/v1alpha1" ||
			object.GetKind() != "Stage" ||
			!slices.Contains(stageNames, object.GetName()) {
			return nil, fmt.Errorf(
				"embedded KWOK asset contains unexpected %s %s/%s",
				object.GetKind(),
				object.GetAPIVersion(),
				object.GetName(),
			)
		}
		object.SetAnnotations(map[string]string{
			ownershipAnnotation: ownershipRoot,
			stageDigestKey:      lock.StageSHA256,
		})
		object.SetLabels(map[string]string{
			managedByLabel: managedByValue,
		})
		stages = append(stages, object)
	}
	slices.SortFunc(stages, func(left, right *unstructured.Unstructured) int {
		switch {
		case left.GetName() < right.GetName():
			return -1
		case left.GetName() > right.GetName():
			return 1
		default:
			return 0
		}
	})
	if len(stages) != len(stageNames) {
		return nil, fmt.Errorf(
			"embedded KWOK asset has %d Stages, want %d",
			len(stages),
			len(stageNames),
		)
	}
	for _, name := range stageNames {
		if !slices.ContainsFunc(stages, func(stage *unstructured.Unstructured) bool {
			return stage.GetName() == name
		}) {
			return nil, fmt.Errorf("embedded KWOK asset is missing Stage %q", name)
		}
	}
	return stages, nil
}

func requireCompatible(stage *unstructured.Unstructured) error {
	actual := stage.GetAnnotations()[ownershipAnnotation]
	if actual != ownershipRoot {
		return fmt.Errorf(
			"refusing to adopt incompatible Stage/%s: ownership root %q, expected %q",
			stage.GetName(),
			actual,
			ownershipRoot,
		)
	}
	return nil
}
