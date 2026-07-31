package kubernetes

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
)

func TestBoundPodObservationSharesOneBudgetAcrossNodes(t *testing.T) {
	t.Parallel()

	kubernetesClient := kubernetesfake.NewSimpleClientset(
		budgetPod("node-a-0", "node-a"),
		budgetPod("node-a-1", "node-a"),
		budgetPod("node-b-0", "node-b"),
		budgetPod("node-b-1", "node-b"),
	)
	adapter := NewAdapter(kubernetesClient)

	_, err := adapter.observePodsBoundToNodes(
		context.Background(),
		[]string{"node-a", "node-b"},
		3,
	)
	if cluster.ErrorCodeOf(err) != cluster.ErrorCapabilityUnavailable {
		t.Fatalf(
			"shared Pod budget error = %v, want CapabilityUnavailable",
			err,
		)
	}
}

func budgetPod(name, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
	}
}
