package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultRefreshInterval = 15 * time.Second
	defaultStaleAfter      = 45 * time.Second
	// MaximumNodes and MaximumDevices bound one immutable observation before
	// allocation or exposition. Source Adapters use the same limits so an
	// untrusted status quantity cannot force unbounded device expansion.
	MaximumNodes   = 1000
	MaximumDevices = 8000
)

// SnapshotSource is the single true external seam used by the module. Its
// production Adapter observes Kubernetes; deterministic tests use memory.
type SnapshotSource interface {
	Snapshot(context.Context) (Observation, error)
}

// Observation is one bounded, internally consistent view of Kasim ownership.
type Observation struct {
	Nodes   []Node
	Devices []Device
}

// Node is one exactly owned Synthetic Node. It is not a physical host.
type Node struct {
	InstanceName string
	InstanceUID  string
	Name         string
	Group        string
}

// Device is one stable simulated Accelerator unit on an observed Node.
type Device struct {
	InstanceName string
	InstanceUID  string
	NodeName     string
	NodeGroup    string
	Pool         string
	ProfileID    string
	ModelID      string
	Ordinal      uint64
	Healthy      bool
}

// Dependencies are the required production boundaries. The listener is
// supplied by process composition so tests exercise the real HTTP protocol.
type Dependencies struct {
	Source    SnapshotSource
	Contracts Catalog
	Listener  net.Listener
}

// Options control sampling and staleness. Now is an internal test clock; the
// zero value uses wall time.
type Options struct {
	RefreshInterval time.Duration
	StaleAfter      time.Duration
	Now             func() time.Time
}

// DefaultOptions returns the supported sampling contract.
func DefaultOptions() Options {
	return Options{
		RefreshInterval: defaultRefreshInterval,
		StaleAfter:      defaultStaleAfter,
		Now:             time.Now,
	}
}

type renderedState struct {
	body          []byte
	diagnostic    []byte
	lastSuccessAt time.Time
	ready         bool
}

// Module hides Kubernetes refresh, deterministic value synthesis, Prometheus
// exposition, immutable caching, staleness, HTTP safety, and shutdown.
type Module struct {
	source       SnapshotSource
	contracts    Catalog
	listener     net.Listener
	refresh      time.Duration
	staleAfter   time.Duration
	now          func() time.Time
	state        atomic.Pointer[renderedState]
	running      atomic.Bool
	renderErrors atomic.Uint64
}

// New validates all dependencies before opening the HTTP lifecycle.
func New(dependencies Dependencies, options Options) (*Module, error) {
	if dependencies.Source == nil {
		return nil, fmt.Errorf("telemetry requires a Snapshot Source")
	}
	if dependencies.Listener == nil {
		return nil, fmt.Errorf("telemetry requires a listener")
	}
	if dependencies.Contracts.revision == "" || len(dependencies.Contracts.profiles) == 0 {
		return nil, fmt.Errorf("telemetry requires a validated contract catalog")
	}
	if options.RefreshInterval == 0 {
		options.RefreshInterval = defaultRefreshInterval
	}
	if options.StaleAfter == 0 {
		options.StaleAfter = defaultStaleAfter
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.RefreshInterval < time.Second || options.RefreshInterval > time.Minute {
		return nil, fmt.Errorf("telemetry refresh interval must be between 1s and 1m")
	}
	if options.StaleAfter < 2*options.RefreshInterval || options.StaleAfter > 10*time.Minute {
		return nil, fmt.Errorf("telemetry stale-after must be between two refresh intervals and 10m")
	}
	module := &Module{
		source:     dependencies.Source,
		contracts:  dependencies.Contracts,
		listener:   dependencies.Listener,
		refresh:    options.RefreshInterval,
		staleAfter: options.StaleAfter,
		now:        options.Now,
	}
	module.state.Store(&renderedState{
		diagnostic: module.diagnosticExposition(false, "initializing"),
	})
	return module, nil
}

// Run owns refresh and HTTP lifecycle until context cancellation or a server
// failure. Scrapes never perform Kubernetes I/O.
func (module *Module) Run(ctx context.Context) error {
	if !module.running.CompareAndSwap(false, true) {
		return fmt.Errorf("telemetry module is already running")
	}
	defer module.running.Store(false)

	server := &http.Server{
		Handler:           module.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		err := server.Serve(module.listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
		close(serverErrors)
	}()

	module.refreshOnce(ctx)
	ticker := time.NewTicker(module.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := server.Shutdown(shutdownContext)
			cancel()
			if err != nil {
				return fmt.Errorf("shutdown telemetry server: %w", err)
			}
			return nil
		case err, open := <-serverErrors:
			if !open {
				return nil
			}
			return fmt.Errorf("serve telemetry: %w", err)
		case <-ticker.C:
			module.refreshOnce(ctx)
		}
	}
}

func (module *Module) refreshOnce(ctx context.Context) {
	observation, err := module.source.Snapshot(ctx)
	now := module.now().UTC()
	if err != nil {
		module.renderErrors.Add(1)
		previous := module.state.Load()
		module.state.Store(&renderedState{
			body:          markExpositionFailure(previous.body, "source-error", module.renderErrors.Load()),
			diagnostic:    module.diagnosticExposition(false, "source-error"),
			lastSuccessAt: previous.lastSuccessAt,
			ready:         false,
		})
		return
	}
	body, err := module.render(observation, now)
	if err != nil {
		module.renderErrors.Add(1)
		previous := module.state.Load()
		module.state.Store(&renderedState{
			body:          markExpositionFailure(previous.body, "render-error", module.renderErrors.Load()),
			diagnostic:    module.diagnosticExposition(false, "render-error"),
			lastSuccessAt: previous.lastSuccessAt,
			ready:         false,
		})
		return
	}
	module.state.Store(&renderedState{
		body:          body,
		diagnostic:    module.diagnosticExposition(true, "ready"),
		lastSuccessAt: now,
		ready:         true,
	})
}

func (module *Module) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, request *http.Request) {
		if !readMethod(writer, request) {
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		if request.Method != http.MethodHead {
			_, _ = writer.Write([]byte("ok\n"))
		}
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, request *http.Request) {
		if !readMethod(writer, request) {
			return
		}
		state := module.state.Load()
		ready := state != nil && state.ready &&
			module.now().UTC().Sub(state.lastSuccessAt) <= module.staleAfter
		if !ready {
			http.Error(writer, "telemetry is not ready", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
		if request.Method != http.MethodHead {
			_, _ = writer.Write([]byte("ok\n"))
		}
	})
	mux.HandleFunc("/metrics", func(writer http.ResponseWriter, request *http.Request) {
		if !readMethod(writer, request) {
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		state := module.state.Load()
		if state == nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fresh := len(state.body) != 0 &&
			module.now().UTC().Sub(state.lastSuccessAt) <= module.staleAfter
		body := state.diagnostic
		if fresh {
			body = state.body
		}
		writer.WriteHeader(http.StatusOK)
		if request.Method != http.MethodHead {
			_, _ = writer.Write(body)
		}
	})
	return mux
}

func markExpositionFailure(body []byte, reason string, errors uint64) []byte {
	if len(body) == 0 {
		return nil
	}
	lines := strings.Split(string(body), "\n")
	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, "kasim_telemetry_source_up "):
			lines[index] = fmt.Sprintf("kasim_telemetry_source_up{reason=%q} 0", reason)
		case strings.HasPrefix(line, "kasim_telemetry_render_errors_total "):
			lines[index] = fmt.Sprintf("kasim_telemetry_render_errors_total %d", errors)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func readMethod(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func validateObservation(observation Observation) error {
	if len(observation.Nodes) > MaximumNodes {
		return fmt.Errorf("observation has %d Nodes; maximum is %d", len(observation.Nodes), MaximumNodes)
	}
	if len(observation.Devices) > MaximumDevices {
		return fmt.Errorf("observation has %d devices; maximum is %d", len(observation.Devices), MaximumDevices)
	}
	nodeKeys := make(map[string]struct{}, len(observation.Nodes))
	for _, node := range observation.Nodes {
		if node.InstanceName == "" || node.InstanceUID == "" || node.Name == "" || node.Group == "" {
			return fmt.Errorf("observed Node identity is incomplete")
		}
		key := node.InstanceUID + "\x00" + node.Name
		if _, duplicate := nodeKeys[key]; duplicate {
			return fmt.Errorf("observed Node %q is duplicated", node.Name)
		}
		nodeKeys[key] = struct{}{}
	}
	deviceKeys := make(map[string]struct{}, len(observation.Devices))
	for _, device := range observation.Devices {
		if device.InstanceName == "" || device.InstanceUID == "" || device.NodeName == "" ||
			device.NodeGroup == "" || device.Pool == "" || device.ProfileID == "" || device.ModelID == "" {
			return fmt.Errorf("observed device identity is incomplete")
		}
		if _, found := nodeKeys[device.InstanceUID+"\x00"+device.NodeName]; !found {
			return fmt.Errorf("device references unobserved Node %q", device.NodeName)
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d", device.InstanceUID, device.NodeName, device.Pool, device.Ordinal)
		if _, duplicate := deviceKeys[key]; duplicate {
			return fmt.Errorf("observed device %q is duplicated", key)
		}
		deviceKeys[key] = struct{}{}
	}
	return nil
}

func sortedObservation(input Observation) Observation {
	result := Observation{
		Nodes:   append([]Node(nil), input.Nodes...),
		Devices: append([]Device(nil), input.Devices...),
	}
	slices.SortFunc(result.Nodes, func(left, right Node) int {
		return compareIdentity(left.InstanceUID, left.Name, right.InstanceUID, right.Name)
	})
	slices.SortFunc(result.Devices, func(left, right Device) int {
		leftKey := fmt.Sprintf("%s\x00%s\x00%s\x00%020d", left.InstanceUID, left.NodeName, left.Pool, left.Ordinal)
		rightKey := fmt.Sprintf("%s\x00%s\x00%s\x00%020d", right.InstanceUID, right.NodeName, right.Pool, right.Ordinal)
		if leftKey < rightKey {
			return -1
		}
		if leftKey > rightKey {
			return 1
		}
		return 0
	})
	return result
}

func compareIdentity(leftA, leftB, rightA, rightB string) int {
	left := leftA + "\x00" + leftB
	right := rightA + "\x00" + rightB
	return strings.Compare(left, right)
}
