// PROTOTYPE: the smallest in-cluster reconciler needed to compare with KWOK.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	serviceAccountDirectory = "/var/run/secrets/kubernetes.io/serviceaccount"
	nodeSelector            = "sim.kube-accelerator.io/managed-by=prototype-12,sim.kube-accelerator.io/runtime=native"
	instanceLabel           = "sim.kube-accelerator.io/instance-uid"
	leaseNamespace          = "kube-node-lease"
)

type metadata struct {
	Name            string            `json:"name"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type nodeList struct {
	Items []struct {
		Metadata metadata `json:"metadata"`
	} `json:"items"`
}

type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func main() {
	client, err := newAPIClient()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("PROTOTYPE native reconciler started selector=%q", nodeSelector)
	for {
		if err := reconcile(client); err != nil {
			log.Printf("reconcile failed: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

func newAPIClient() (*apiClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	if host == "" || port == "" {
		return nil, fmt.Errorf("Kubernetes service environment is missing")
	}

	token, err := os.ReadFile(serviceAccountDirectory + "/token")
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	ca, err := os.ReadFile(serviceAccountDirectory + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("parse service account CA")
	}

	return &apiClient{
		baseURL: "https://" + host + ":" + port,
		token:   string(token),
		http: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
					RootCAs:    roots,
				},
			},
		},
	}, nil
}

func reconcile(client *apiClient) error {
	path := "/api/v1/nodes?labelSelector=" + url.QueryEscape(nodeSelector)
	body, status, err := client.request(http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("list nodes returned %d: %s", status, body)
	}

	var nodes nodeList
	if err := json.Unmarshal(body, &nodes); err != nil {
		return fmt.Errorf("decode node list: %w", err)
	}

	for _, node := range nodes.Items {
		instanceUID := node.Metadata.Labels[instanceLabel]
		if instanceUID == "" {
			log.Printf("skip node=%s: missing %s", node.Metadata.Name, instanceLabel)
			continue
		}
		if err := client.reconcileNode(node.Metadata.Name); err != nil {
			log.Printf("node=%s: %v", node.Metadata.Name, err)
			continue
		}
		if err := client.reconcileLease(node.Metadata.Name, instanceUID); err != nil {
			log.Printf("lease=%s: %v", node.Metadata.Name, err)
		}
	}
	return nil
}

func (client *apiClient) reconcileNode(name string) error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z07:00")
	patch := map[string]any{
		"status": map[string]any{
			"phase": "Running",
			"conditions": []map[string]string{
				{
					"type":               "Ready",
					"status":             "True",
					"reason":             "NativePrototypeReady",
					"message":            "Ready is maintained by the issue 12 native prototype",
					"lastHeartbeatTime":  now,
					"lastTransitionTime": now,
				},
			},
		},
	}
	body, status, err := client.request(
		http.MethodPatch,
		"/api/v1/nodes/"+url.PathEscape(name)+"/status",
		"application/merge-patch+json",
		patch,
	)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("patch node status returned %d: %s", status, body)
	}
	return nil
}

func (client *apiClient) reconcileLease(name, instanceUID string) error {
	path := "/apis/coordination.k8s.io/v1/namespaces/" + leaseNamespace + "/leases/" + url.PathEscape(name)
	_, status, err := client.request(http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z07:00")
	labels := map[string]string{
		"sim.kube-accelerator.io/managed-by":   "prototype-12",
		"sim.kube-accelerator.io/runtime":      "native",
		"sim.kube-accelerator.io/instance-uid": instanceUID,
	}
	if status == http.StatusNotFound {
		lease := map[string]any{
			"apiVersion": "coordination.k8s.io/v1",
			"kind":       "Lease",
			"metadata": map[string]any{
				"name":      name,
				"namespace": leaseNamespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"holderIdentity":       "kas-native-prototype/" + name,
				"leaseDurationSeconds": 40,
				"acquireTime":          now,
				"renewTime":            now,
			},
		}
		body, createStatus, createErr := client.request(
			http.MethodPost,
			"/apis/coordination.k8s.io/v1/namespaces/"+leaseNamespace+"/leases",
			"application/json",
			lease,
		)
		if createErr != nil {
			return createErr
		}
		if createStatus < 200 || createStatus >= 300 {
			return fmt.Errorf("create lease returned %d: %s", createStatus, body)
		}
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("get lease returned %d", status)
	}

	patch := map[string]any{
		"metadata": map[string]any{"labels": labels},
		"spec": map[string]any{
			"holderIdentity":       "kas-native-prototype/" + name,
			"leaseDurationSeconds": 40,
			"renewTime":            now,
		},
	}
	body, patchStatus, patchErr := client.request(
		http.MethodPatch,
		path,
		"application/merge-patch+json",
		patch,
	)
	if patchErr != nil {
		return patchErr
	}
	if patchStatus < 200 || patchStatus >= 300 {
		return fmt.Errorf("patch lease returned %d: %s", patchStatus, body)
	}
	return nil
}

func (client *apiClient) request(method, path, contentType string, value any) ([]byte, int, error) {
	var body io.Reader
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(data)
	}

	request, err := http.NewRequest(method, client.baseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}

	response, err := client.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	return data, response.StatusCode, nil
}
