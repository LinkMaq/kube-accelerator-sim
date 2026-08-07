package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	schedulingcatalog "github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
)

func TestBundledCatalogHasEvidenceGatedCoverage(t *testing.T) {
	t.Parallel()

	catalog, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled() error = %v", err)
	}
	if catalog.Revision() != "2026-08-07" || !strings.HasPrefix(catalog.Digest(), "sha256:") {
		t.Fatalf("unexpected catalog identity: %s %s", catalog.Revision(), catalog.Digest())
	}
	states := catalog.ProfileStates()
	for _, profile := range []string{
		"nvidia", "amd", "intel-gpu", "huawei-ascend", "cambricon",
		"iluvatar", "enflame", "furiosa", "rdma-shared-device-plugin",
	} {
		if states[profile] != "verified" {
			t.Errorf("profile %s state = %q, want verified", profile, states[profile])
		}
	}
	for _, profile := range []string{"intel-gaudi", "aws-neuron", "google-tpu", "metax"} {
		if states[profile] != "provisional" {
			t.Errorf("profile %s state = %q, want provisional", profile, states[profile])
		}
	}
	for _, profile := range []string{"hygon", "kunlunxin-hami", "sriov-network-device-plugin"} {
		if states[profile] != "unavailable" {
			t.Errorf("profile %s state = %q, want unavailable", profile, states[profile])
		}
	}
}

func TestTelemetryCatalogClassifiesEverySchedulingProfile(t *testing.T) {
	t.Parallel()

	telemetryCatalog, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	schedulingCatalog, err := schedulingcatalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	states := telemetryCatalog.ProfileStates()
	for _, profile := range schedulingCatalog.List() {
		if states[profile.ID()] == "" {
			t.Errorf("scheduling profile %s has no telemetry classification", profile.ID())
		}
	}
}

func TestRenderIsDeterministicCorrelatedAndParseable(t *testing.T) {
	t.Parallel()

	module := testModule(t)
	observation := testObservation("nvidia", "nvidia-h200", 2, 1)
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	first, err := module.render(observation, at)
	if err != nil {
		t.Fatalf("render first: %v", err)
	}
	second, err := module.render(observation, at.Add(14*time.Second))
	if err != nil {
		t.Fatalf("render second: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("same sample bucket produced different exposition")
	}
	third, err := module.render(observation, at.Add(15*time.Second))
	if err != nil {
		t.Fatalf("render next bucket: %v", err)
	}
	if string(first) == string(third) {
		t.Fatal("next sample bucket did not evolve")
	}

	families := parseExposition(t, first)
	for _, required := range []string{
		"DCGM_FI_DEV_GPU_UTIL", "DCGM_FI_DEV_FB_USED", "DCGM_FI_DEV_FB_FREE",
		"DCGM_FI_DEV_POWER_USAGE", "DCGM_FI_DEV_GPU_TEMP",
		"kasim_telemetry_node_info", "kasim_telemetry_device_contract_available",
	} {
		if families[required] == nil {
			t.Errorf("exposition lacks %s", required)
		}
	}
	utilization := families["DCGM_FI_DEV_GPU_UTIL"].Metric
	if len(utilization) != 2 {
		t.Fatalf("GPU utilization samples = %d, want 2", len(utilization))
	}
	for _, sample := range utilization {
		value := sample.GetGauge().GetValue()
		if value < 0 || value > 100 {
			t.Errorf("utilization = %v, want [0,100]", value)
		}
		labels := metricLabels(sample)
		if labels["kasim_simulated"] != "true" || labels["Hostname"] != "kasim-node-a" {
			t.Errorf("native/provenance labels = %#v", labels)
		}
	}
	used := families["DCGM_FI_DEV_FB_USED"].Metric[0].GetGauge().GetValue()
	free := families["DCGM_FI_DEV_FB_FREE"].Metric[0].GetGauge().GetValue()
	if used < 0 || free < 0 || used+free > 144384.000001 {
		t.Errorf("H200 memory invariant failed: used=%v free=%v", used, free)
	}
	if unavailable := metricValueByLabel(
		t,
		families["kasim_telemetry_device_contract_available"],
		"kasim_device",
		syntheticIdentity(observation.Devices[1]),
	); unavailable != 1 {
		t.Errorf("verified contract available = %v, want 1", unavailable)
	}
}

func TestCentralizedEndpointAttributesDevicesToSyntheticNodes(t *testing.T) {
	t.Parallel()

	module := testModule(t)
	observation := Observation{
		Nodes: []Node{
			{InstanceName: "lab", InstanceUID: "instance-uid", Name: "kasim-node-a", Group: "workers"},
			{InstanceName: "lab", InstanceUID: "instance-uid", Name: "kasim-node-b", Group: "workers"},
		},
	}
	for _, node := range observation.Nodes {
		observation.Devices = append(observation.Devices, Device{
			InstanceName: node.InstanceName, InstanceUID: node.InstanceUID,
			NodeName: node.Name, NodeGroup: node.Group, Pool: "accelerators",
			ProfileID: "nvidia", ModelID: "nvidia-h200", Healthy: true,
		})
	}

	families := parseExpositionAt(
		t,
		module,
		observation,
		time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	)
	samples := families["DCGM_FI_DEV_GPU_UTIL"].Metric
	if len(samples) != 2 {
		t.Fatalf("GPU utilization samples = %d, want 2", len(samples))
	}
	for _, sample := range samples {
		labels := metricLabels(sample)
		if labels["node"] == "" || labels["node"] != labels["Hostname"] ||
			labels["node"] != labels["kasim_node"] {
			t.Errorf("device node identity labels = %#v", labels)
		}
	}
}

func TestRenderReportsUnavailableWithoutInventingNativeMetrics(t *testing.T) {
	t.Parallel()

	module := testModule(t)
	body, err := module.render(testObservation("hygon", "hygon-k100-ai", 1, 1), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	families := parseExposition(t, body)
	if families["kasim_telemetry_device_contract_available"] == nil {
		t.Fatal("unavailable profile lacks explicit diagnostic")
	}
	for name := range families {
		if strings.Contains(strings.ToLower(name), "hygon") || strings.Contains(strings.ToLower(name), "dcu") {
			t.Fatalf("unavailable profile invented native family %q", name)
		}
	}
}

func TestCountersAreMonotonicAcrossBuckets(t *testing.T) {
	t.Parallel()

	module := testModule(t)
	observation := testObservation("nvidia", "nvidia-h200", 1, 1)
	first := parseExpositionAt(t, module, observation, time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC))
	second := parseExpositionAt(t, module, observation, time.Date(2026, 8, 7, 12, 1, 0, 0, time.UTC))
	name := "DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION"
	left := first[name].Metric[0].GetCounter().GetValue()
	right := second[name].Metric[0].GetCounter().GetValue()
	if right <= left {
		t.Fatalf("counter did not increase: %v -> %v", left, right)
	}
}

func TestModuleServesCachedMetricsAndReadiness(t *testing.T) {
	t.Parallel()

	catalog, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	module, err := New(Dependencies{
		Source:    memorySource{observation: testObservation("amd", "amd-mi300x", 1, 1)},
		Contracts: catalog,
		Listener:  listener,
	}, Options{RefreshInterval: time.Second, StaleAfter: 2 * time.Second, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- module.Run(ctx) }()

	baseURL := "http://" + listener.Addr().String()
	body := eventuallyGET(t, baseURL+"/metrics")
	if !strings.Contains(body, "gpu_gfx_activity") || !strings.Contains(body, `kasim_simulated="true"`) {
		t.Fatalf("unexpected metrics body:\n%s", body)
	}
	if ready := eventuallyStatus(t, baseURL+"/readyz"); ready != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", ready)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d, want 405", response.StatusCode)
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestSourceFailureKeepsBoundedLastSuccessThenDropsNativeSeries(t *testing.T) {
	catalog, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	module, err := New(Dependencies{
		Source:    memorySource{err: fmt.Errorf("temporary source failure")},
		Contracts: catalog, Listener: listener,
	}, Options{RefreshInterval: time.Second, StaleAfter: 2 * time.Second, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	body, err := module.render(testObservation("nvidia", "nvidia-h200", 1, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	module.state.Store(&renderedState{body: body, lastSuccessAt: now, ready: true})
	module.refreshOnce(context.Background())

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	module.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "DCGM_FI_DEV_GPU_UTIL") ||
		!strings.Contains(response.Body.String(), `kasim_telemetry_source_up{reason="source-error"} 0`) {
		t.Fatalf("grace response = %d\n%s", response.Code, response.Body.String())
	}
	ready := httptest.NewRecorder()
	module.handler().ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness after source failure = %d, want 503", ready.Code)
	}

	now = now.Add(3 * time.Second)
	stale := httptest.NewRecorder()
	module.handler().ServeHTTP(stale, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if stale.Code != http.StatusOK || strings.Contains(stale.Body.String(), "DCGM_FI_DEV_GPU_UTIL") ||
		!strings.Contains(stale.Body.String(), "kasim_telemetry_source_up") {
		t.Fatalf("stale response = %d\n%s", stale.Code, stale.Body.String())
	}
}

func TestObservationBudgetFailsClosed(t *testing.T) {
	t.Parallel()

	module := testModule(t)
	observation := Observation{Nodes: make([]Node, MaximumNodes+1)}
	if _, err := module.render(observation, time.Now()); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized observation error = %v", err)
	}
}

func TestReferenceScaleRendersWithoutPerDeviceRuntimeState(t *testing.T) {
	module := testModule(t)
	observation := Observation{
		Nodes:   make([]Node, 0, MaximumNodes),
		Devices: make([]Device, 0, MaximumDevices),
	}
	for nodeIndex := 0; nodeIndex < MaximumNodes; nodeIndex++ {
		node := Node{
			InstanceName: "scale-lab", InstanceUID: "scale-instance",
			Name: fmt.Sprintf("kasim-node-%04d", nodeIndex), Group: "workers",
		}
		observation.Nodes = append(observation.Nodes, node)
		for deviceIndex := 0; deviceIndex < 8; deviceIndex++ {
			observation.Devices = append(observation.Devices, Device{
				InstanceName: node.InstanceName, InstanceUID: node.InstanceUID,
				NodeName: node.Name, NodeGroup: node.Group, Pool: "accelerators",
				ProfileID: "nvidia", ModelID: "nvidia-h200",
				Ordinal: uint64(deviceIndex), Healthy: true,
			})
		}
	}
	body, err := module.render(
		observation,
		time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if count := bytes.Count(body, []byte("DCGM_FI_DEV_GPU_UTIL{")); count != MaximumDevices {
		t.Fatalf("GPU utilization series = %d, want %d", count, MaximumDevices)
	}
	if len(body) > 128<<20 {
		t.Fatalf("reference exposition = %d bytes, want <= 128 MiB", len(body))
	}
}

type memorySource struct {
	observation Observation
	err         error
}

func (source memorySource) Snapshot(context.Context) (Observation, error) {
	return source.observation, source.err
}

func testModule(t *testing.T) *Module {
	t.Helper()
	catalog, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	module, err := New(Dependencies{
		Source: memorySource{}, Contracts: catalog, Listener: listener,
	}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func testObservation(profile, model string, total, healthy int) Observation {
	node := Node{InstanceName: "lab", InstanceUID: "instance-uid", Name: "kasim-node-a", Group: "workers"}
	result := Observation{Nodes: []Node{node}}
	for index := 0; index < total; index++ {
		result.Devices = append(result.Devices, Device{
			InstanceName: node.InstanceName, InstanceUID: node.InstanceUID,
			NodeName: node.Name, NodeGroup: node.Group, Pool: "accelerators",
			ProfileID: profile, ModelID: model, Ordinal: uint64(index), Healthy: index < healthy,
		})
	}
	return result
}

func parseExpositionAt(t *testing.T, module *Module, observation Observation, at time.Time) map[string]*dto.MetricFamily {
	t.Helper()
	body, err := module.render(observation, at)
	if err != nil {
		t.Fatal(err)
	}
	return parseExposition(t, body)
}

func parseExposition(t *testing.T, body []byte) map[string]*dto.MetricFamily {
	t.Helper()
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("parse Prometheus exposition: %v\n%s", err, body)
	}
	return families
}

func metricLabels(metric interface{ GetLabel() []*dto.LabelPair }) map[string]string {
	result := make(map[string]string)
	for _, label := range metric.GetLabel() {
		result[label.GetName()] = label.GetValue()
	}
	return result
}

func metricValueByLabel(t *testing.T, family *dto.MetricFamily, labelName, labelValue string) float64 {
	t.Helper()
	for _, metric := range family.Metric {
		if metricLabels(metric)[labelName] == labelValue {
			if metric.Gauge != nil {
				return metric.Gauge.GetValue()
			}
			if metric.Counter != nil {
				return metric.Counter.GetValue()
			}
		}
	}
	t.Fatalf("metric label %s=%s not found", labelName, labelValue)
	return 0
}

func eventuallyGET(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && len(body) > 0 {
				return string(body)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GET %s did not become ready", url)
	return ""
}

func eventuallyStatus(t *testing.T, url string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	last := 0
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			last = response.StatusCode
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if last == http.StatusOK {
				return last
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return last
}
