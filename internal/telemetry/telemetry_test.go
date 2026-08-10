package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"sort"
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
	if catalog.Revision() != "2026-08-10.1" || !strings.HasPrefix(catalog.Digest(), "sha256:") {
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
		"DCGM_FI_DEV_SM_CLOCK", "DCGM_FI_DEV_MEM_CLOCK", "DCGM_FI_DEV_MEMORY_TEMP",
		"DCGM_FI_DEV_GPU_TEMP", "DCGM_FI_DEV_POWER_USAGE",
		"DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION", "DCGM_FI_DEV_PCIE_REPLAY_COUNTER",
		"DCGM_FI_DEV_GPU_UTIL", "DCGM_FI_DEV_MEM_COPY_UTIL", "DCGM_FI_DEV_ENC_UTIL",
		"DCGM_FI_DEV_DEC_UTIL", "DCGM_FI_DEV_XID_ERRORS", "DCGM_FI_DEV_FB_FREE",
		"DCGM_FI_DEV_FB_USED", "DCGM_FI_DEV_FB_RESERVED",
		"DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS", "DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS",
		"DCGM_FI_DEV_ROW_REMAP_FAILURE", "DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL",
		"DCGM_FI_DEV_VGPU_LICENSE_STATUS",
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
		assertLabelKeys(t, labels, []string{
			"gpu", "UUID", "pci_bus_id", "device", "modelName", "Hostname",
			"DCGM_FI_DRIVER_VERSION", "node",
		})
		if labels["Hostname"] != "kasim-node-a" || labels["node"] != "kasim-node-a" ||
			labels["device"] != "nvidia"+labels["gpu"] ||
			labels["modelName"] != "nvidia-h200" || labels["DCGM_FI_DRIVER_VERSION"] != "580.126.16" ||
			!strings.HasPrefix(labels["UUID"], "GPU-") ||
			!strings.HasPrefix(labels["pci_bus_id"], "00000000:") {
			t.Errorf("DCGM native label values = %#v", labels)
		}
	}
	xidLabels := metricLabels(families["DCGM_FI_DEV_XID_ERRORS"].Metric[0])
	assertLabelKeys(t, xidLabels, []string{
		"gpu", "UUID", "pci_bus_id", "device", "modelName", "Hostname",
		"DCGM_FI_DRIVER_VERSION", "err_code", "err_msg", "node",
	})
	if xidLabels["err_code"] != "0" || xidLabels["err_msg"] != "No Error" {
		t.Errorf("DCGM XID labels = %#v", xidLabels)
	}
	if got := families["DCGM_FI_DEV_SM_CLOCK"].GetHelp(); got != "SM clock frequency (in MHz)." {
		t.Errorf("DCGM HELP = %q", got)
	}
	if got := families["DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION"].GetType(); got != dto.MetricType_COUNTER {
		t.Errorf("DCGM energy TYPE = %s, want COUNTER", got)
	}
	used := families["DCGM_FI_DEV_FB_USED"].Metric[0].GetGauge().GetValue()
	free := families["DCGM_FI_DEV_FB_FREE"].Metric[0].GetGauge().GetValue()
	reserved := families["DCGM_FI_DEV_FB_RESERVED"].Metric[0].GetGauge().GetValue()
	if used < 0 || free < 0 || reserved < 0 || used+free+reserved > 144384.000001 {
		t.Errorf("H200 memory invariant failed: used=%v free=%v reserved=%v", used, free, reserved)
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

func TestNVIDIAUnhealthyDeviceUsesNativeXIDErrorSchema(t *testing.T) {
	t.Parallel()

	families := parseExpositionAt(
		t,
		testModule(t),
		testObservation("nvidia", "nvidia-h200", 1, 0),
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	)
	sample := families["DCGM_FI_DEV_XID_ERRORS"].Metric[0]
	labels := metricLabels(sample)
	if labels["err_code"] != "79" || labels["err_msg"] != "GPU has fallen off the bus" ||
		sample.GetGauge().GetValue() != 79 {
		t.Errorf("unhealthy XID sample = labels %#v value %v", labels, sample.GetGauge().GetValue())
	}
}

func TestNVIDIARenderMatchesSuppliedDCGMExpositionSchema(t *testing.T) {
	t.Parallel()

	referenceBody, err := os.ReadFile("testdata/dcgm-exporter-runtime.prom")
	if err != nil {
		t.Fatal(err)
	}
	reference := parseExposition(t, referenceBody)
	actual := parseExpositionAt(
		t,
		testModule(t),
		testObservation("nvidia", "nvidia-h200", 1, 1),
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	)
	for name, want := range reference {
		got := actual[name]
		if got == nil {
			t.Errorf("rendered exposition lacks supplied DCGM family %s", name)
			continue
		}
		if got.GetType() != want.GetType() {
			t.Errorf("%s TYPE = %s, want %s", name, got.GetType(), want.GetType())
		}
		if got.GetHelp() != want.GetHelp() {
			t.Errorf("%s HELP = %q, want %q", name, got.GetHelp(), want.GetHelp())
		}
		wantLabels := metricLabels(want.Metric[0])
		wantKeys := make([]string, 0, len(wantLabels))
		for label := range wantLabels {
			wantKeys = append(wantKeys, label)
		}
		assertLabelKeys(t, metricLabels(got.Metric[0]), append(wantKeys, "node"))
	}
}

func TestCentralizedEndpointAttributesEveryDeviceToItsSyntheticNode(t *testing.T) {
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
		if labels["Hostname"] == "" || labels["node"] != labels["Hostname"] {
			t.Errorf("device native node identity labels = %#v", labels)
		}
		for name := range labels {
			if strings.HasPrefix(name, "kasim_") {
				t.Errorf("vendor-native sample leaked non-native label %q: %#v", name, labels)
			}
		}
	}
}

func TestEveryVerifiedProfileEmitsOnlyCatalogDeclaredNativeLabels(t *testing.T) {
	t.Parallel()

	module := testModule(t)
	for _, profileID := range []string{
		"nvidia", "amd", "intel-gpu", "huawei-ascend", "cambricon",
		"iluvatar", "enflame", "furiosa", "rdma-shared-device-plugin",
	} {
		profile, found := module.contracts.profile(profileID)
		if !found {
			t.Fatalf("profile %s not found", profileID)
		}
		modelID := ""
		if len(profile.Models) > 0 {
			modelID = profile.Models[0].ID
		} else {
			modelID = profileID
		}
		families := parseExpositionAt(
			t,
			module,
			testObservation(profileID, modelID, 1, 1),
			time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		)
		for _, family := range profile.MetricFamily {
			samples := families[family.Name].Metric
			if len(samples) != 1 {
				t.Fatalf("profile %s family %s sample count = %d", profileID, family.Name, len(samples))
			}
			want := make([]string, 0, len(profile.DeviceLabels)+len(family.Labels)+1)
			for _, label := range profile.DeviceLabels {
				want = append(want, label.Name)
			}
			for _, label := range family.Labels {
				want = append(want, label.Name)
			}
			if !slices.Contains(want, "node") {
				want = append(want, "node")
			}
			assertLabelKeys(t, metricLabels(samples[0]), want)
		}
	}
}

func TestNativeRDMADeviceLabelsRemainUniqueAcrossSyntheticNodes(t *testing.T) {
	t.Parallel()

	observation := Observation{
		Nodes: []Node{
			{InstanceName: "lab", InstanceUID: "instance-uid", Name: "kasim-node-a", Group: "workers"},
			{InstanceName: "lab", InstanceUID: "instance-uid", Name: "kasim-node-b", Group: "workers"},
		},
	}
	for _, node := range observation.Nodes {
		observation.Devices = append(observation.Devices, Device{
			InstanceName: node.InstanceName, InstanceUID: node.InstanceUID,
			NodeName: node.Name, NodeGroup: node.Group, Pool: "rdma",
			ProfileID: "rdma-shared-device-plugin", ModelID: "rdma-shared-device-plugin",
			Healthy: true,
		})
	}
	families := parseExpositionAt(
		t,
		testModule(t),
		observation,
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	)
	samples := families["node_infiniband_rate_bytes_per_second"].Metric
	if len(samples) != 2 {
		t.Fatalf("RDMA samples = %d, want 2", len(samples))
	}
	left := metricLabels(samples[0])
	right := metricLabels(samples[1])
	assertLabelKeys(t, left, []string{"device", "port", "node"})
	assertLabelKeys(t, right, []string{"device", "port", "node"})
	if left["device"] == right["device"] {
		t.Fatalf("aggregate endpoint produced duplicate native RDMA series labels: %#v", left)
	}
	if left["node"] == right["node"] {
		t.Fatalf("aggregate endpoint collapsed distinct Synthetic Nodes: %#v %#v", left, right)
	}
}

func TestNVIDIAIdentityLabelsStayStableAcrossAllDeviceMetricFamilies(t *testing.T) {
	t.Parallel()

	families := parseExpositionAt(
		t,
		testModule(t),
		testObservation("nvidia", "nvidia-h200", 2, 2),
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	)
	identities := map[string]string{}
	for name, family := range families {
		if !strings.HasPrefix(name, "DCGM_FI_DEV_") {
			continue
		}
		for _, sample := range family.Metric {
			labels := metricLabels(sample)
			gpu := labels["gpu"]
			if gpu == "" || labels["UUID"] == "" {
				t.Fatalf("%s lacks stable device identity labels: %#v", name, labels)
			}
			identity := strings.Join([]string{
				labels["UUID"], labels["node"], labels["Hostname"], labels["modelName"],
			}, "|")
			if previous, found := identities[gpu]; found && previous != identity {
				t.Fatalf("GPU %s identity changed across metric families: %q != %q", gpu, previous, identity)
			}
			identities[gpu] = identity
		}
	}
	if len(identities) != 2 {
		t.Fatalf("stable GPU identities = %#v, want two devices", identities)
	}
}

func TestCompatibilityValueConventions(t *testing.T) {
	t.Parallel()

	percentage, supported := metricValue(
		metricFamily{Semantic: "utilization-ratio"},
		Device{Healthy: true},
		simulationLimits{},
		0.75,
		time.Time{},
	)
	if !supported || percentage != 75 {
		t.Fatalf("utilization-ratio = %v supported=%t, want 75/true", percentage, supported)
	}
	for _, semantic := range []string{"health-enflame", "health-binary", "last-error"} {
		healthy, healthySupported := metricValue(
			metricFamily{Semantic: semantic}, Device{Healthy: true}, simulationLimits{}, 0.5, time.Time{},
		)
		unhealthy, unhealthySupported := metricValue(
			metricFamily{Semantic: semantic}, Device{Healthy: false}, simulationLimits{}, 0.5, time.Time{},
		)
		if !healthySupported || healthy != 0 {
			t.Errorf("%s healthy = %v supported=%t, want 0/true", semantic, healthy, healthySupported)
		}
		if !unhealthySupported || unhealthy == 0 {
			t.Errorf("%s unhealthy = %v supported=%t, want non-zero/true", semantic, unhealthy, unhealthySupported)
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
	if !strings.Contains(body, "gpu_gfx_activity") || strings.Contains(nativeMetricLine(body, "gpu_gfx_activity"), "kasim_") {
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

func assertLabelKeys(t *testing.T, labels map[string]string, want []string) {
	t.Helper()
	got := make([]string, 0, len(labels))
	for name := range labels {
		got = append(got, name)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("label keys = %v, want %v", got, want)
	}
}

func nativeMetricLine(body, metricName string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, metricName+"{") || strings.HasPrefix(line, metricName+" ") {
			return line
		}
	}
	return ""
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
