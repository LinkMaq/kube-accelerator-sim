package kubernetes

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controlleroptions "sigs.k8s.io/controller-runtime/pkg/controller"
	controllerreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/reconcile"
)

const MaximumConcurrentReconciles = 16

type DeliveryOptions struct {
	Client                  client.Client
	Module                  *reconcile.InstanceReconciler
	MaxConcurrentReconciles int
	ProgressRequeueAfter    time.Duration
}

// Delivery translates one shared controller-runtime queue event into exactly
// one call to the deep Instance Reconciler Module.
type Delivery struct {
	client                  client.Client
	module                  *reconcile.InstanceReconciler
	maxConcurrentReconciles int
	progressRequeueAfter    time.Duration
}

func NewDelivery(options DeliveryOptions) (*Delivery, error) {
	if options.MaxConcurrentReconciles <= 0 ||
		options.MaxConcurrentReconciles > MaximumConcurrentReconciles {
		return nil, fmt.Errorf(
			"controller concurrency must be between 1 and %d",
			MaximumConcurrentReconciles,
		)
	}
	if options.Client == nil || options.Module == nil {
		return nil, fmt.Errorf(
			"controller delivery requires a shared-cache client and reconciler module",
		)
	}
	if options.ProgressRequeueAfter <= 0 {
		options.ProgressRequeueAfter = time.Second
	}
	return &Delivery{
		client:                  options.Client,
		module:                  options.Module,
		maxConcurrentReconciles: options.MaxConcurrentReconciles,
		progressRequeueAfter:    options.ProgressRequeueAfter,
	}, nil
}

func (delivery *Delivery) Reconcile(
	ctx context.Context,
	request ctrl.Request,
) (ctrl.Result, error) {
	instance := &simulationv1alpha1.ScenarioInstance{}
	if err := delivery.client.Get(ctx, request.NamespacedName, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf(
			"read queued Scenario Instance from shared cache: %w",
			err,
		)
	}
	name, err := domain.ParseName(instance.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("queued Scenario Instance name: %w", err)
	}
	fingerprint, err := domain.ParseDigest(instance.Spec.TargetFingerprint)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"queued Scenario Instance target fingerprint: %w",
			err,
		)
	}
	result, err := delivery.module.Reconcile(ctx, controlplane.InstanceKey{
		TargetFingerprint: fingerprint,
		Name:              name,
	})
	return deliveryOutcome(result, err, delivery.progressRequeueAfter)
}

func deliveryOutcome(
	result reconcile.Result,
	err error,
	progressRequeueAfter time.Duration,
) (ctrl.Result, error) {
	if err != nil {
		// A status writer conflict means the Scenario Instance changed after
		// the module read its snapshot. Discard the stale intent and retry from
		// a fresh snapshot without surfacing a controller error.
		if isStatusCommitConflict(err) {
			return ctrl.Result{
				RequeueAfter: progressRequeueAfter,
			}, nil
		}
		if result.Requeue() || result.Phase() == "" {
			return ctrl.Result{}, err
		}
		// A non-retryable post-acceptance failure is durable status. A later
		// accepted revision or deletion event will enqueue the object again.
		return ctrl.Result{}, nil
	}
	if result.Requeue() {
		return ctrl.Result{
			RequeueAfter: progressRequeueAfter,
		}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager uses the manager's one shared cache and one bounded
// workqueue; it creates no Node-, pool-, or instance-specific informer.
func (delivery *Delivery) SetupWithManager(manager ctrl.Manager) error {
	if delivery == nil || manager == nil {
		return fmt.Errorf("controller delivery requires a manager")
	}
	return ctrl.NewControllerManagedBy(manager).
		For(&simulationv1alpha1.ScenarioInstance{}).
		WithOptions(controlleroptions.Options{
			MaxConcurrentReconciles: delivery.maxConcurrentReconciles,
		}).
		Complete(delivery)
}

var _ controllerreconcile.Reconciler = (*Delivery)(nil)
