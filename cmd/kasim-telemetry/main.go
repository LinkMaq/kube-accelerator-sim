package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/telemetry"
	telemetrykubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/telemetry/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/version"
)

type options struct {
	listenAddress   string
	refreshInterval time.Duration
	staleAfter      time.Duration
	showVersion     bool
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
	schedulingCatalog, err := catalog.LoadBundled()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load bundled scheduling catalog: %v\n", err)
		return 1
	}
	telemetryCatalog, err := telemetry.LoadBundled()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load bundled telemetry catalog: %v\n", err)
		return 1
	}
	if options.showVersion {
		fmt.Print(version.HumanWithCatalog("kasim-telemetry", schedulingCatalog.Revision()))
		fmt.Printf("telemetry-catalog=%s digest=%s\n", telemetryCatalog.Revision(), telemetryCatalog.Digest())
		return 0
	}
	if err := start(options, schedulingCatalog, telemetryCatalog); err != nil {
		fmt.Fprintf(os.Stderr, "kasim-telemetry: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(arguments []string) (options, error) {
	defaults := telemetry.DefaultOptions()
	result := options{
		listenAddress:   ":9400",
		refreshInterval: defaults.RefreshInterval,
		staleAfter:      defaults.StaleAfter,
	}
	flags := flag.NewFlagSet("kasim-telemetry", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&result.listenAddress, "listen-address", result.listenAddress,
		"address for metrics, health, and readiness HTTP endpoints")
	flags.DurationVar(&result.refreshInterval, "refresh-interval", result.refreshInterval,
		"interval between immutable simulated telemetry samples")
	flags.DurationVar(&result.staleAfter, "stale-after", result.staleAfter,
		"maximum age of a successful Kubernetes observation")
	flags.BoolVar(&result.showVersion, "version", false, "print version metadata")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("kasim-telemetry accepts flags only, got %q", flags.Arg(0))
	}
	if result.listenAddress == "" {
		return options{}, fmt.Errorf("listen-address must not be empty")
	}
	if result.refreshInterval < time.Second || result.refreshInterval > time.Minute {
		return options{}, fmt.Errorf("refresh-interval must be between 1s and 1m")
	}
	if result.staleAfter < 2*result.refreshInterval || result.staleAfter > 10*time.Minute {
		return options{}, fmt.Errorf("stale-after must be between two refresh intervals and 10m")
	}
	return result, nil
}

func start(
	options options,
	schedulingCatalog catalog.Snapshot,
	telemetryCatalog telemetry.Catalog,
) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register core/v1 scheme: %w", err)
	}
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register simulation.kasim.io/v1alpha1 scheme: %w", err)
	}
	reader, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("construct read-only Kubernetes client: %w", err)
	}
	source, err := telemetrykubernetes.New(reader, schedulingCatalog)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", options.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", options.listenAddress, err)
	}
	module, err := telemetry.New(telemetry.Dependencies{
		Source: source, Contracts: telemetryCatalog, Listener: listener,
	}, telemetry.Options{
		RefreshInterval: options.refreshInterval,
		StaleAfter:      options.staleAfter,
	})
	if err != nil {
		_ = listener.Close()
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return module.Run(ctx)
}
