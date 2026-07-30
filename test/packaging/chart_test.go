package packaging_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	productVersion = "0.1.0"
	chartPath      = "../../charts/kasim-runtime"
)

type manifest struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   map[string]any `yaml:"metadata"`
	Spec       map[string]any `yaml:"spec"`
	Rules      []rbacRule     `yaml:"rules"`
}

type rbacRule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

func TestChartLintsAndRendersAcrossSupportedKubernetesRange(t *testing.T) {
	helm := requireHelm(t)

	run(t, helm, "lint", chartPath, "--strict")
	for _, version := range []string{
		"1.30.14",
		"1.31.14",
		"1.32.13",
		"1.33.13",
		"1.34.10",
		"1.35.7",
		"1.36.3",
	} {
		version := version
		t.Run(version, func(t *testing.T) {
			rendered := run(
				t,
				helm,
				"template",
				"contract",
				chartPath,
				"--namespace",
				"kasim-system",
				"--kube-version",
				version,
			)
			if len(decodeManifests(t, rendered)) == 0 {
				t.Fatal("chart rendered no Kubernetes objects")
			}
		})
	}
	for _, unsupported := range []string{"1.29.14", "1.37.0"} {
		command := exec.Command(
			helm,
			"template",
			"contract",
			chartPath,
			"--kube-version",
			unsupported,
		)
		if output, err := command.CombinedOutput(); err == nil {
			t.Errorf(
				"chart accepted unsupported Kubernetes %s:\n%s",
				unsupported,
				output,
			)
		}
	}
}

func TestChartRendersOwnershipBoundedSecureRuntime(t *testing.T) {
	helm := requireHelm(t)
	rendered := run(
		t,
		helm,
		"template",
		"contract",
		chartPath,
		"--namespace",
		"kasim-system",
	)
	objects := decodeManifests(t, rendered)

	if objectByKindName(objects, "Namespace", "kasim-system") != nil {
		t.Fatal("runtime chart must not create its target namespace")
	}
	controller := requireObject(
		t,
		objects,
		"Deployment",
		"contract-kasim-runtime-controller",
	)
	kwok := requireObject(
		t,
		objects,
		"Deployment",
		"contract-kasim-runtime-kwok-controller",
	)
	assertSecureDeployment(t, controller)
	assertSecureDeployment(t, kwok)
	assertRealNodeAffinity(t, controller)
	assertRealNodeAffinity(t, kwok)
	for _, name := range []string{
		"contract-kasim-runtime-stage-apply",
		"contract-kasim-runtime-stage-delete",
	} {
		job := requireObject(t, objects, "Job", name)
		assertSecurePodTemplate(t, objectName(job), mapValue(t, job.Spec, "template"))
		assertRealNodeAffinity(t, job)
	}

	for _, roleName := range []string{
		"contract-kasim-runtime-observer",
		"contract-kasim-runtime-operator",
		"contract-kasim-runtime-controller",
		"contract-kasim-runtime-kwok-controller",
		"contract-kasim-runtime-stage-installer",
	} {
		role := requireObject(t, objects, "ClusterRole", roleName)
		assertNoWildcards(t, role)
	}
	controllerRole := requireObject(
		t,
		objects,
		"ClusterRole",
		"contract-kasim-runtime-controller",
	)
	assertControllerDenyList(t, controllerRole)
}

func TestVendoredRuntimeAssetsMatchCanonicalLocks(t *testing.T) {
	productCRD, err := os.ReadFile(
		"../../config/crd/bases/simulation.kasim.io_scenarioinstances.yaml",
	)
	if err != nil {
		t.Fatal(err)
	}
	chartCRD, err := os.ReadFile(
		filepath.Join(chartPath, "crds/scenarioinstances.simulation.kasim.io.yaml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(productCRD, chartCRD) {
		t.Fatal("chart product CRD drifted from the canonical generated CRD")
	}
	productCRDSum := sha256.Sum256(chartCRD)
	if got, want := fmt.Sprintf("%x", productCRDSum[:]),
		"6edf364b62e22de7deb05e817a865601cfce0af9943a73f85c4a8bddb0d15be9"; got != want {
		t.Fatalf("product CRD SHA-256 = %s, want %s", got, want)
	}

	stages, err := os.ReadFile(filepath.Join(chartPath, "assets/stage-fast.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(stages)
	if got, want := fmt.Sprintf("%x", sum[:]),
		"2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24"; got != want {
		t.Fatalf("vendored Stage SHA-256 = %s, want %s", got, want)
	}
	stageCRD, err := os.ReadFile(
		filepath.Join(chartPath, "crds/stages.kwok.x-k8s.io.yaml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	stageCRDSum := sha256.Sum256(stageCRD)
	if got, want := fmt.Sprintf("%x", stageCRDSum[:]),
		"7140ccc35f9e3733a013bd89ba34c9252e2444b6c2cc2275e632e4decd605bd6"; got != want {
		t.Fatalf("vendored Stage CRD SHA-256 = %s, want %s", got, want)
	}
	for _, receipt := range []string{
		"simulation.kasim.io/ownership-root: kasim-runtime/v1alpha1",
		"simulation.kasim.io/kwok-version: v0.8.0",
		"simulation.kasim.io/kwok-manifest-sha256: a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405",
	} {
		if !bytes.Contains(stageCRD, []byte(receipt)) {
			t.Errorf("vendored Stage CRD lacks %q", receipt)
		}
	}
	ownershipCheck, err := os.ReadFile(
		filepath.Join(chartPath, "templates/_helpers.tpl"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"lookup",
		"refusing to adopt incompatible",
		"scenarioinstances.simulation.kasim.io",
		"stages.kwok.x-k8s.io",
	} {
		if !bytes.Contains(ownershipCheck, []byte(required)) {
			t.Errorf("ownership preflight lacks %q", required)
		}
	}
}

func TestChartVersionsAndImmutableRuntimeInputsStayExplicit(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	var chart struct {
		Version     string            `yaml:"version"`
		AppVersion  string            `yaml:"appVersion"`
		Annotations map[string]string `yaml:"annotations"`
	}
	if err := yaml.Unmarshal(encoded, &chart); err != nil {
		t.Fatalf("decode Chart.yaml: %v", err)
	}
	if chart.Version != productVersion || chart.AppVersion != productVersion {
		t.Fatalf(
			"chart version/appVersion = %q/%q, want %s/%s",
			chart.Version,
			chart.AppVersion,
			productVersion,
			productVersion,
		)
	}
	for key, want := range map[string]string{
		"simulation.kasim.io/schema-version":        "v1alpha1",
		"simulation.kasim.io/product-crd-sha256":    "6edf364b62e22de7deb05e817a865601cfce0af9943a73f85c4a8bddb0d15be9",
		"simulation.kasim.io/catalog-revision":      "2026-07-30",
		"simulation.kasim.io/kubernetes-range":      "1.30-1.36",
		"simulation.kasim.io/kwok-version":          "v0.8.0",
		"simulation.kasim.io/kwok-manifest-sha256":  "a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405",
		"simulation.kasim.io/kwok-stage-sha256":     "2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24",
		"simulation.kasim.io/kwok-stage-crd-sha256": "7140ccc35f9e3733a013bd89ba34c9252e2444b6c2cc2275e632e4decd605bd6",
	} {
		if got := chart.Annotations[key]; got != want {
			t.Errorf("Chart annotation %q = %q, want %q", key, got, want)
		}
	}

	helm := requireHelm(t)
	command := exec.Command(
		helm,
		"template",
		"contract",
		chartPath,
		"--set",
		"controller.image.tag=9.9.9",
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("chart accepted drifting image version:\n%s", output)
	}
	command = exec.Command(
		helm,
		"template",
		"contract",
		chartPath,
		"--set",
		"controller.replicas=2",
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("chart accepted active replicas without leader election:\n%s", output)
	}
}

func TestChartContractGolden(t *testing.T) {
	helm := requireHelm(t)
	rendered := run(
		t,
		helm,
		"template",
		"contract",
		chartPath,
		"--namespace",
		"kasim-system",
	)
	objects := decodeManifests(t, rendered)
	contract := make([]string, 0, len(objects))
	for _, object := range objects {
		name, _ := object.Metadata["name"].(string)
		contract = append(
			contract,
			fmt.Sprintf("%s/%s", object.Kind, name),
		)
	}
	slices.Sort(contract)
	actual := strings.Join(contract, "\n") + "\n"
	goldenPath := "testdata/chart-contract.golden"
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read chart contract golden: %v", err)
	}
	if actual != string(want) {
		t.Fatalf("chart contract drifted\nwant:\n%s\ngot:\n%s", want, actual)
	}
}

func TestContainerBuildContract(t *testing.T) {
	encoded, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	source := string(encoded)
	for _, required := range []string{
		"FROM --platform=$BUILDPLATFORM",
		"ARG TARGETOS",
		"ARG TARGETARCH",
		"CGO_ENABLED=0",
		"USER 65532:65532",
		`org.opencontainers.image.version`,
		`org.opencontainers.image.revision`,
		`org.opencontainers.image.created`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("Dockerfile does not contain %q", required)
		}
	}
}

func requireHelm(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("helm")
	if err != nil {
		t.Fatal("Helm is required for packaging contract tests")
	}
	return path
}

func run(t *testing.T, name string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command(name, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v failed: %v\n%s", command.Args, err, output)
	}
	return output
}

func decodeManifests(t *testing.T, encoded []byte) []manifest {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	var objects []manifest
	for {
		var object manifest
		err := decoder.Decode(&object)
		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			t.Fatalf("decode rendered manifest: %v", err)
		}
		if object.Kind != "" {
			objects = append(objects, object)
		}
	}
	return objects
}

func requireObject(
	t *testing.T,
	objects []manifest,
	kind string,
	name string,
) manifest {
	t.Helper()
	object := objectByKindName(objects, kind, name)
	if object == nil {
		t.Fatalf("rendered chart has no %s/%s", kind, name)
	}
	return *object
}

func objectByKindName(
	objects []manifest,
	kind string,
	name string,
) *manifest {
	for index := range objects {
		objectName, _ := objects[index].Metadata["name"].(string)
		if objects[index].Kind == kind && objectName == name {
			return &objects[index]
		}
	}
	return nil
}

func assertSecureDeployment(t *testing.T, deployment manifest) {
	t.Helper()
	podTemplate := mapValue(t, deployment.Spec, "template")
	assertSecurePodTemplate(t, objectName(deployment), podTemplate)
}

func assertSecurePodTemplate(
	t *testing.T,
	name string,
	podTemplate map[string]any,
) {
	t.Helper()
	podSpec := mapValue(t, podTemplate, "spec")
	podSecurity := mapValue(t, podSpec, "securityContext")
	if got := podSecurity["runAsNonRoot"]; got != true {
		t.Errorf("%s pod runAsNonRoot = %#v, want true", name, got)
	}
	containers := sliceValue(t, podSpec, "containers")
	for _, raw := range containers {
		container, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s container has type %T", name, raw)
		}
		security := mapValue(t, container, "securityContext")
		for key, want := range map[string]any{
			"allowPrivilegeEscalation": false,
			"privileged":               false,
			"readOnlyRootFilesystem":   true,
			"runAsNonRoot":             true,
		} {
			if got := security[key]; got != want {
				t.Errorf(
					"%s container %s = %#v, want %#v",
					name,
					key,
					got,
					want,
				)
			}
		}
	}
}

func assertRealNodeAffinity(t *testing.T, deployment manifest) {
	t.Helper()
	podTemplate := mapValue(t, deployment.Spec, "template")
	podSpec := mapValue(t, podTemplate, "spec")
	affinity := mapValue(t, podSpec, "affinity")
	nodeAffinity := mapValue(t, affinity, "nodeAffinity")
	required := mapValue(
		t,
		nodeAffinity,
		"requiredDuringSchedulingIgnoredDuringExecution",
	)
	terms := sliceValue(t, required, "nodeSelectorTerms")
	if len(terms) != 1 {
		t.Fatalf("%s has %d required node selector terms, want 1", objectName(deployment), len(terms))
	}
	term, ok := terms[0].(map[string]any)
	if !ok {
		t.Fatalf("%s node selector term has type %T", objectName(deployment), terms[0])
	}
	expressions := sliceValue(t, term, "matchExpressions")
	wants := map[string]string{
		"app.kubernetes.io/managed-by":     "NotIn",
		"simulation.kasim.io/instance-uid": "DoesNotExist",
	}
	for key, operator := range wants {
		found := false
		for _, raw := range expressions {
			expression, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if expression["key"] == key && expression["operator"] == operator {
				found = true
			}
		}
		if !found {
			t.Errorf(
				"%s lacks hard real-Node exclusion %s/%s",
				objectName(deployment),
				key,
				operator,
			)
		}
	}
}

func assertNoWildcards(t *testing.T, role manifest) {
	t.Helper()
	for _, rule := range role.Rules {
		for _, values := range [][]string{rule.APIGroups, rule.Resources, rule.Verbs} {
			if slices.Contains(values, "*") {
				t.Errorf("%s contains wildcard RBAC rule %#v", objectName(role), rule)
			}
		}
	}
}

func assertControllerDenyList(t *testing.T, role manifest) {
	t.Helper()
	encoded, err := json.Marshal(role.Rules)
	if err != nil {
		t.Fatalf("encode controller RBAC: %v", err)
	}
	source := string(encoded)
	for _, forbidden := range []string{
		`"secrets"`,
		`"serviceaccounts/token"`,
		`"clusterroles"`,
		`"clusterrolebindings"`,
		`"customresourcedefinitions"`,
		`"pods/eviction"`,
		`"impersonate"`,
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s grants forbidden permission %s", objectName(role), forbidden)
		}
	}
}

func mapValue(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("%q has type %T, want map", key, object[key])
	}
	return value
}

func sliceValue(t *testing.T, object map[string]any, key string) []any {
	t.Helper()
	value, ok := object[key].([]any)
	if !ok {
		t.Fatalf("%q has type %T, want slice", key, object[key])
	}
	return value
}

func objectName(object manifest) string {
	name, _ := object.Metadata["name"].(string)
	return object.Kind + "/" + name
}
