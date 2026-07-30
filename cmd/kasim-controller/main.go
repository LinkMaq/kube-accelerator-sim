package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	clusterkubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/cluster/kubernetes"
	controlplanekubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/selecting"
	"github.com/LinkMaq/kube-accelerator-sim/internal/reconcile"
	reconcilekubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/reconcile/kubernetes"
	kwokkubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/runtime/kwok/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/version"
)

const leaderElectionID = "kasim-controller.simulation.kasim.io"

type options struct {
	metricsAddress          string
	healthAddress           string
	leaderElection          bool
	maxConcurrentReconciles int
	showVersion             bool
	kwokStageOperation      string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	options, err := parseOptions(arguments)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load bundled catalog: %v\n", err)
		return 1
	}
	if options.showVersion {
		fmt.Print(version.HumanWithCatalog(
			"kasim-controller",
			snapshot.Revision(),
		))
		return 0
	}
	if options.kwokStageOperation != "" {
		if err := runKWOKStageOperation(options.kwokStageOperation); err != nil {
			fmt.Fprintf(os.Stderr, "kasim-controller: %v\n", err)
			return 1
		}
		return 0
	}
	if err := start(options, snapshot); err != nil {
		fmt.Fprintf(os.Stderr, "kasim-controller: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(arguments []string) (options, error) {
	result := options{}
	flags := flag.NewFlagSet("kasim-controller", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(
		&result.metricsAddress,
		"metrics-bind-address",
		":8080",
		"address for the controller metrics endpoint",
	)
	flags.StringVar(
		&result.healthAddress,
		"health-probe-bind-address",
		":8081",
		"address for health and readiness probes",
	)
	flags.BoolVar(
		&result.leaderElection,
		"leader-elect",
		false,
		"enable controller leader election",
	)
	flags.IntVar(
		&result.maxConcurrentReconciles,
		"max-concurrent-reconciles",
		8,
		"bounded number of Scenario Instances reconciled concurrently",
	)
	flags.BoolVar(&result.showVersion, "version", false, "print version metadata")
	flags.StringVar(
		&result.kwokStageOperation,
		"kwok-stage-operation",
		"",
		"internal Helm hook operation: apply or delete",
	)
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf(
			"kasim-controller accepts flags only, got %q",
			flags.Arg(0),
		)
	}
	if result.maxConcurrentReconciles < 1 ||
		result.maxConcurrentReconciles >
			reconcilekubernetes.MaximumConcurrentReconciles {
		return options{}, fmt.Errorf(
			"max-concurrent-reconciles must be between 1 and %d",
			reconcilekubernetes.MaximumConcurrentReconciles,
		)
	}
	switch result.kwokStageOperation {
	case "", "apply", "delete":
	default:
		return options{}, fmt.Errorf(
			"kwok-stage-operation must be apply or delete",
		)
	}
	return result, nil
}

func runKWOKStageOperation(operation string) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("construct KWOK Stage client: %w", err)
	}
	switch operation {
	case "apply":
		return kwokkubernetes.ApplyStages(context.Background(), client)
	case "delete":
		return kwokkubernetes.DeleteStages(context.Background(), client)
	default:
		return fmt.Errorf("unsupported KWOK Stage operation %q", operation)
	}
}

func start(options options, snapshot catalog.Snapshot) error {
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	scheme := runtime.NewScheme()
	for name, add := range map[string]func(*runtime.Scheme) error{
		"core/v1":                      corev1.AddToScheme,
		"coordination.k8s.io/v1":       coordinationv1.AddToScheme,
		"resource.k8s.io/v1":           resourcev1.AddToScheme,
		"simulation.kasim.io/v1alpha1": simulationv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			return fmt.Errorf("register %s scheme: %w", name, err)
		}
	}

	manager, err := ctrl.NewManager(config, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: options.metricsAddress,
		},
		HealthProbeBindAddress: options.healthAddress,
		LeaderElection:         options.leaderElection,
		LeaderElectionID:       leaderElectionID,
	})
	if err != nil {
		return fmt.Errorf("construct controller manager: %w", err)
	}
	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("register health check: %w", err)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("register readiness check: %w", err)
	}

	directClient, err := client.NewWithWatch(config, client.Options{
		Scheme:     scheme,
		HTTPClient: manager.GetHTTPClient(),
		Mapper:     manager.GetRESTMapper(),
	})
	if err != nil {
		return fmt.Errorf("construct direct watch client: %w", err)
	}
	kubernetesClient, err := clientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("construct typed Kubernetes client: %w", err)
	}
	controlPlane := controlplanekubernetes.New(
		watchingClient{
			Client:      manager.GetClient(),
			watchClient: directClient,
		},
		version.SchemaVersion,
	)
	statusWriter := reconcilekubernetes.NewStatusWriter(manager.GetClient())
	module, err := reconcile.New(reconcile.Options{
		ControlPlane: controlPlane,
		Cluster:      clusterkubernetes.NewAdapter(kubernetesClient),
		Catalog:      snapshot,
		Projection:   selecting.New(),
		Now:          time.Now,
		Commit:       statusWriter.Commit,
	})
	if err != nil {
		return fmt.Errorf("construct Instance Reconciler: %w", err)
	}
	delivery, err := reconcilekubernetes.NewDelivery(
		reconcilekubernetes.DeliveryOptions{
			Client:                  manager.GetClient(),
			Module:                  module,
			MaxConcurrentReconciles: options.maxConcurrentReconciles,
			ProgressRequeueAfter:    time.Second,
		},
	)
	if err != nil {
		return fmt.Errorf("construct controller delivery: %w", err)
	}
	if err := delivery.SetupWithManager(manager); err != nil {
		return fmt.Errorf("register Scenario Instance controller: %w", err)
	}
	if err := manager.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("run controller manager: %w", err)
	}
	return nil
}

type watchingClient struct {
	client.Client
	watchClient client.WithWatch
}

func (combined watchingClient) Watch(
	ctx context.Context,
	object client.ObjectList,
	options ...client.ListOption,
) (watch.Interface, error) {
	return combined.watchClient.Watch(ctx, object, options...)
}
