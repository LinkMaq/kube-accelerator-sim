// PROTOTYPE: concurrent Kubernetes API driver for issue #4.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	managedByKey = "sim.kube-accelerator.io/managed-by"
	instanceKey  = "sim.kube-accelerator.io/instance-uid"
	managedBy    = "scale-prototype-4"
	instanceUID  = "issue-4-scale"
	nodePrefix   = "kas-scale-node-"
	podPrefix    = "kas-scale-pod-"
	gpuResource  = "nvidia.com/gpu"
	workloadNS   = "kas-scale-workloads"
)

type metadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

type condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

type nodeList struct {
	Items []struct {
		Metadata metadata `json:"metadata"`
		Status   struct {
			Capacity    map[string]string `json:"capacity"`
			Allocatable map[string]string `json:"allocatable"`
			Conditions  []condition       `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type objectList struct {
	Items []struct {
		Metadata metadata `json:"metadata"`
		Status   struct {
			Phase string `json:"phase"`
		} `json:"status"`
	} `json:"items"`
}

type result struct {
	Timestamp      string         `json:"timestamp"`
	Operation      string         `json:"operation"`
	Requested      int            `json:"requested,omitempty"`
	Completed      int            `json:"completed,omitempty"`
	DurationMillis int64          `json:"durationMillis"`
	Details        map[string]any `json:"details,omitempty"`
	Error          string         `json:"error,omitempty"`
}

type client struct {
	base string
	http *http.Client
}

var (
	command     = flag.String("command", "", "operation to perform")
	baseURL     = flag.String("base-url", "", "kubectl proxy base URL")
	count       = flag.Int("count", 1000, "expected or requested object count")
	concurrency = flag.Int("concurrency", 32, "maximum concurrent requests")
	sample      = flag.Int("sample", 100, "sample size")
	healthy     = flag.Int("healthy", 8, "healthy Accelerator count")
	timeout     = flag.Duration("timeout", 5*time.Minute, "convergence timeout")
	iterations  = flag.Int("iterations", 10, "observation samples")
)

func main() {
	flag.Parse()
	if *command == "" || *baseURL == "" {
		fmt.Fprintln(os.Stderr, "-command and -base-url are required")
		os.Exit(2)
	}

	c := &client{
		base: *baseURL,
		http: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        *concurrency * 2,
				MaxIdleConnsPerHost: *concurrency * 2,
			},
		},
	}

	start := time.Now()
	r := result{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Operation: *command,
		Requested: *count,
	}
	var err error

	switch *command {
	case "apply-nodes":
		err = c.applyNodes(*count, *concurrency)
		r.Completed = *count
	case "wait-ready":
		r.Details, err = c.waitNodesReady(*count, *timeout)
		r.Completed = detailInt(r.Details, "readyNodes")
	case "label-leases":
		err = c.labelLeases(*count, *concurrency, *timeout)
		r.Completed = *count
	case "observe":
		r.Details, err = c.observe(*count, *iterations)
		r.Completed = detailInt(r.Details, "nodes")
	case "patch-health":
		r.Completed, err = c.patchHealth(*sample, *healthy, *concurrency, *timeout)
		r.Requested = *sample
		r.Details = map[string]any{"healthyPerNode": *healthy}
	case "patch-ready-false":
		r.Completed, err = c.patchReadyFalse(*sample, *concurrency)
		r.Requested = *sample
	case "apply-pods":
		err = c.applyPods(*count, *concurrency)
		r.Completed = *count
	case "wait-pods":
		r.Details, err = c.waitPods(*count, *timeout)
		r.Completed = detailInt(r.Details, "runningPods")
	case "delete-pods":
		err = c.deleteCollection(
			"/api/v1/namespaces/"+workloadNS+"/pods",
			managedSelector(),
			*timeout,
		)
		r.Completed = *count
	case "delete-nodes":
		err = c.deleteCollection("/api/v1/nodes", managedSelector(), *timeout)
		r.Completed = *count
	case "delete-leases":
		err = c.deleteCollection(
			"/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases",
			managedSelector(),
			*timeout,
		)
		r.Completed = *count
	default:
		err = fmt.Errorf("unknown command %q", *command)
	}

	r.DurationMillis = time.Since(start).Milliseconds()
	if err != nil {
		r.Error = err.Error()
		emit(r)
		os.Exit(1)
	}
	emit(r)
}

func emit(r result) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(r); err != nil {
		panic(err)
	}
}

func managedSelector() string {
	return managedByKey + "=" + managedBy + "," + instanceKey + "=" + instanceUID
}

func detailInt(details map[string]any, key string) int {
	if details == nil {
		return 0
	}
	value, _ := details[key].(int)
	return value
}

func (c *client) applyNodes(total, workers int) error {
	return parallel(total, workers, func(index int) error {
		name := fmt.Sprintf("%s%04d", nodePrefix, index)
		object := map[string]any{
			"apiVersion": "v1",
			"kind":       "Node",
			"metadata": map[string]any{
				"name": name,
				"annotations": map[string]string{
					"kwok.x-k8s.io/node": "fake",
				},
				"labels": map[string]string{
					managedByKey:             managedBy,
					instanceKey:              instanceUID,
					"kubernetes.io/hostname": name,
					"kubernetes.io/arch":     "amd64",
					"kubernetes.io/os":       "linux",
				},
			},
			"spec": map[string]any{},
			"status": map[string]any{
				"phase": "Running",
				"capacity": map[string]string{
					"cpu":       "16",
					"memory":    "64Gi",
					"pods":      "110",
					gpuResource: "8",
				},
				"allocatable": map[string]string{
					"cpu":       "16",
					"memory":    "64Gi",
					"pods":      "110",
					gpuResource: "8",
				},
				"nodeInfo": map[string]string{
					"architecture":            "amd64",
					"containerRuntimeVersion": "kwok",
					"kubeletVersion":          "kwok",
					"operatingSystem":         "linux",
					"osImage":                 "kwok",
				},
			},
		}
		status, body, err := c.request(http.MethodPost, "/api/v1/nodes", object)
		if err != nil {
			return err
		}
		if status == http.StatusCreated || status == http.StatusConflict {
			return nil
		}
		return fmt.Errorf("create node %s returned %d: %s", name, status, body)
	})
}

func (c *client) waitNodesReady(expected int, deadline time.Duration) (map[string]any, error) {
	end := time.Now().Add(deadline)
	var last map[string]any
	for time.Now().Before(end) {
		nodes, elapsed, err := c.listNodes()
		if err != nil {
			return nil, err
		}
		ready := 0
		capacity := 0
		allocatable := 0
		for _, node := range nodes.Items {
			for _, item := range node.Status.Conditions {
				if item.Type == "Ready" && item.Status == "True" {
					ready++
					break
				}
			}
			capacity += quantity(node.Status.Capacity[gpuResource])
			allocatable += quantity(node.Status.Allocatable[gpuResource])
		}
		last = map[string]any{
			"nodes":                len(nodes.Items),
			"readyNodes":           ready,
			"acceleratorCapacity":  capacity,
			"acceleratorAvailable": allocatable,
			"lastListMillis":       elapsed.Milliseconds(),
		}
		if len(nodes.Items) == expected && ready == expected && capacity == expected*8 {
			return last, nil
		}
		time.Sleep(time.Second)
	}
	return last, fmt.Errorf("node convergence timed out: %v", last)
}

func (c *client) labelLeases(expected, workers int, deadline time.Duration) error {
	end := time.Now().Add(deadline)
	var names []string
	for time.Now().Before(end) {
		items, _, err := c.listObjects(
			"/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases",
			"",
		)
		if err != nil {
			return err
		}
		names = names[:0]
		for _, item := range items.Items {
			if len(item.Metadata.Name) >= len(nodePrefix) &&
				item.Metadata.Name[:len(nodePrefix)] == nodePrefix {
				names = append(names, item.Metadata.Name)
			}
		}
		if len(names) == expected {
			break
		}
		time.Sleep(time.Second)
	}
	if len(names) != expected {
		return fmt.Errorf("expected %d leases, found %d", expected, len(names))
	}
	return parallelNames(names, workers, func(name string) error {
		path := "/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases/" +
			url.PathEscape(name)
		patch := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]string{
					managedByKey: managedBy,
					instanceKey:  instanceUID,
				},
			},
		}
		status, body, err := c.request(http.MethodPatch, path, patch)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("label lease %s returned %d: %s", name, status, body)
		}
		return nil
	})
}

func (c *client) observe(expected, count int) (map[string]any, error) {
	durations := make([]int64, 0, count)
	nodesSeen := 0
	capacity := 0
	for range count {
		nodes, elapsed, err := c.listNodes()
		if err != nil {
			return nil, err
		}
		nodesSeen = len(nodes.Items)
		capacity = 0
		for _, node := range nodes.Items {
			capacity += quantity(node.Status.Capacity[gpuResource])
		}
		durations = append(durations, elapsed.Milliseconds())
	}
	if nodesSeen != expected || capacity != expected*8 {
		return nil, fmt.Errorf("observe saw nodes=%d capacity=%d", nodesSeen, capacity)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return map[string]any{
		"nodes":               nodesSeen,
		"acceleratorCapacity": capacity,
		"samples":             len(durations),
		"minMillis":           durations[0],
		"p50Millis":           percentile(durations, 0.50),
		"p95Millis":           percentile(durations, 0.95),
		"maxMillis":           durations[len(durations)-1],
	}, nil
}

func (c *client) patchHealth(limit, value, workers int, deadline time.Duration) (int, error) {
	nodes, _, err := c.listNodes()
	if err != nil {
		return 0, err
	}
	names := nodeNames(nodes)
	if limit > len(names) {
		limit = len(names)
	}
	names = names[:limit]
	err = parallelNames(names, workers, func(name string) error {
		path := "/api/v1/nodes/" + url.PathEscape(name) + "/status"
		patch := map[string]any{
			"status": map[string]any{
				"allocatable": map[string]string{gpuResource: strconv.Itoa(value)},
			},
		}
		status, body, requestErr := c.request(http.MethodPatch, path, patch)
		if requestErr != nil {
			return requestErr
		}
		if status != http.StatusOK {
			return fmt.Errorf("patch node health %s returned %d: %s", name, status, body)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	expected := map[string]struct{}{}
	for _, name := range names {
		expected[name] = struct{}{}
	}
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		current, _, listErr := c.listNodes()
		if listErr != nil {
			return 0, listErr
		}
		matched := 0
		for _, node := range current.Items {
			if _, ok := expected[node.Metadata.Name]; ok &&
				quantity(node.Status.Allocatable[gpuResource]) == value {
				matched++
			}
		}
		if matched == len(names) {
			return matched, nil
		}
		time.Sleep(time.Second)
	}
	return 0, errors.New("health update did not converge")
}

func (c *client) patchReadyFalse(limit, workers int) (int, error) {
	nodes, _, err := c.listNodes()
	if err != nil {
		return 0, err
	}
	names := nodeNames(nodes)
	if limit > len(names) {
		limit = len(names)
	}
	names = names[:limit]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err = parallelNames(names, workers, func(name string) error {
		path := "/api/v1/nodes/" + url.PathEscape(name) + "/status"
		patch := map[string]any{
			"status": map[string]any{
				"conditions": []map[string]string{{
					"type":               "Ready",
					"status":             "False",
					"reason":             "ScalePrototypeRestart",
					"message":            "KWOK restart recovery probe",
					"lastHeartbeatTime":  now,
					"lastTransitionTime": now,
				}},
			},
		}
		status, body, requestErr := c.request(http.MethodPatch, path, patch)
		if requestErr != nil {
			return requestErr
		}
		if status != http.StatusOK {
			return fmt.Errorf("patch Ready %s returned %d: %s", name, status, body)
		}
		return nil
	})
	return len(names), err
}

func (c *client) applyPods(total, workers int) error {
	return parallel(total, workers, func(index int) error {
		name := fmt.Sprintf("%s%04d", podPrefix, index)
		object := map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      name,
				"namespace": workloadNS,
				"labels": map[string]string{
					managedByKey: managedBy,
					instanceKey:  instanceUID,
				},
			},
			"spec": map[string]any{
				"restartPolicy": "Never",
				"nodeSelector": map[string]string{
					managedByKey: managedBy,
					instanceKey:  instanceUID,
				},
				"containers": []map[string]any{{
					"name":  "pause",
					"image": "registry.k8s.io/pause:3.10",
					"resources": map[string]any{
						"requests": map[string]string{gpuResource: "8"},
						"limits":   map[string]string{gpuResource: "8"},
					},
				}},
			},
		}
		path := "/api/v1/namespaces/" + workloadNS + "/pods"
		status, body, err := c.request(http.MethodPost, path, object)
		if err != nil {
			return err
		}
		if status == http.StatusCreated || status == http.StatusConflict {
			return nil
		}
		return fmt.Errorf("create pod %s returned %d: %s", name, status, body)
	})
}

func (c *client) waitPods(expected int, deadline time.Duration) (map[string]any, error) {
	end := time.Now().Add(deadline)
	var last map[string]any
	for time.Now().Before(end) {
		items, elapsed, err := c.listObjects(
			"/api/v1/namespaces/"+workloadNS+"/pods",
			managedSelector(),
		)
		if err != nil {
			return nil, err
		}
		running := 0
		for _, item := range items.Items {
			if item.Status.Phase == "Running" {
				running++
			}
		}
		last = map[string]any{
			"pods":           len(items.Items),
			"runningPods":    running,
			"lastListMillis": elapsed.Milliseconds(),
		}
		if len(items.Items) == expected && running == expected {
			return last, nil
		}
		time.Sleep(time.Second)
	}
	return last, fmt.Errorf("pod convergence timed out: %v", last)
}

func (c *client) deleteCollection(path, selector string, deadline time.Duration) error {
	query := path + "?labelSelector=" + url.QueryEscape(selector)
	options := map[string]any{
		"apiVersion":         "v1",
		"kind":               "DeleteOptions",
		"gracePeriodSeconds": 0,
		"propagationPolicy":  "Background",
	}
	status, body, err := c.request(http.MethodDelete, query, options)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("delete collection returned %d: %s", status, body)
	}
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		items, _, listErr := c.listObjects(path, selector)
		if listErr != nil {
			return listErr
		}
		if len(items.Items) == 0 {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("delete collection did not converge for %s", path)
}

func (c *client) listNodes() (nodeList, time.Duration, error) {
	var nodes nodeList
	start := time.Now()
	status, body, err := c.request(
		http.MethodGet,
		"/api/v1/nodes?labelSelector="+url.QueryEscape(managedSelector()),
		nil,
	)
	elapsed := time.Since(start)
	if err != nil {
		return nodes, elapsed, err
	}
	if status != http.StatusOK {
		return nodes, elapsed, fmt.Errorf("list nodes returned %d: %s", status, body)
	}
	err = json.Unmarshal(body, &nodes)
	return nodes, elapsed, err
}

func (c *client) listObjects(path, selector string) (objectList, time.Duration, error) {
	var items objectList
	if selector != "" {
		path += "?labelSelector=" + url.QueryEscape(selector)
	}
	start := time.Now()
	status, body, err := c.request(http.MethodGet, path, nil)
	elapsed := time.Since(start)
	if err != nil {
		return items, elapsed, err
	}
	if status != http.StatusOK {
		return items, elapsed, fmt.Errorf("list returned %d: %s", status, body)
	}
	err = json.Unmarshal(body, &items)
	return items, elapsed, err
}

func (c *client) request(method, path string, value any) (int, []byte, error) {
	var payload []byte
	var err error
	if value != nil {
		payload, err = json.Marshal(value)
		if err != nil {
			return 0, nil, err
		}
	}

	for attempt := 0; attempt < 6; attempt++ {
		request, requestErr := http.NewRequest(method, c.base+path, bytes.NewReader(payload))
		if requestErr != nil {
			return 0, nil, requestErr
		}
		if value != nil {
			if method == http.MethodPatch {
				request.Header.Set("Content-Type", "application/merge-patch+json")
			} else {
				request.Header.Set("Content-Type", "application/json")
			}
		}
		response, requestErr := c.http.Do(request)
		if requestErr != nil {
			if attempt == 5 {
				return 0, nil, requestErr
			}
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<20))
		response.Body.Close()
		if readErr != nil {
			return 0, nil, readErr
		}
		if response.StatusCode != http.StatusTooManyRequests &&
			response.StatusCode < http.StatusInternalServerError {
			return response.StatusCode, body, nil
		}
		if attempt == 5 {
			return response.StatusCode, body, nil
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	panic("unreachable")
}

func nodeNames(nodes nodeList) []string {
	names := make([]string, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		names = append(names, node.Metadata.Name)
	}
	sort.Strings(names)
	return names
}

func quantity(value string) int {
	number, _ := strconv.Atoi(value)
	return number
}

func percentile(sorted []int64, fraction float64) int64 {
	index := int(float64(len(sorted)-1) * fraction)
	return sorted[index]
}

func parallel(total, workers int, fn func(int) error) error {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	errs := make(chan error, total)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				if err := fn(index); err != nil {
					errs <- err
				}
			}
		}()
	}
	for index := 0; index < total; index++ {
		jobs <- index
	}
	close(jobs)
	group.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func parallelNames(names []string, workers int, fn func(string) error) error {
	return parallel(len(names), workers, func(index int) error {
		return fn(names[index])
	})
}
