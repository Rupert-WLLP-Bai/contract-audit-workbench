package systemhealth

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	serviceAccountDir   = "/var/run/secrets/kubernetes.io/serviceaccount"
	kubernetesBodyLimit = 2 << 20
)

type KubernetesConfig struct {
	APIURL    string
	Token     string
	Namespace string
	Client    *http.Client
	tokenPath string
	initErr   error
}

type Cluster struct {
	Status     string        `json:"status"`
	Namespace  string        `json:"namespace"`
	ObservedAt string        `json:"observed_at"`
	Detail     string        `json:"detail"`
	Nodes      []ClusterNode `json:"nodes"`
}

type ClusterNode struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version"`
}

// KubernetesConfigFromEnv enables observation only when the service environment
// identifies an in-cluster API. It never reads a kubeconfig or external credentials.
func KubernetesConfigFromEnv() *KubernetesConfig {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	if host == "" || port == "" {
		return nil
	}
	config := &KubernetesConfig{}
	config.tokenPath = filepath.Join(serviceAccountDir, "token")
	config.initErr = config.initialize(host, port)
	return config
}

func (config *KubernetesConfig) initialize(host, port string) error {
	apiURL, err := inClusterAPIURL(host, port)
	if err != nil {
		return err
	}
	config.APIURL = apiURL
	config.Token, err = readKubernetesToken(config.tokenPath)
	if err != nil {
		return errors.New("in-cluster service account token unavailable")
	}
	config.Namespace, err = inClusterNamespace(filepath.Dir(config.tokenPath))
	if err != nil {
		return err
	}
	config.Client, err = inClusterClient(filepath.Join(filepath.Dir(config.tokenPath), "ca.crt"))
	return err
}

func inClusterAPIURL(host, port string) (string, error) {
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 || strings.ContainsAny(host, "/?#@") {
		return "", errors.New("invalid in-cluster API address")
	}
	return "https://" + net.JoinHostPort(host, port), nil
}

func readKubernetesToken(path string) (string, error) {
	token, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return "", errors.New("Kubernetes credentials unavailable")
	}
	return strings.TrimSpace(string(token)), nil
}

func inClusterNamespace(directory string) (string, error) {
	namespace := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	if namespace == "" {
		data, err := os.ReadFile(filepath.Join(directory, "namespace"))
		if err == nil {
			namespace = strings.TrimSpace(string(data))
		}
	}
	if namespace == "" {
		return "", errors.New("in-cluster namespace unavailable")
	}
	return namespace, nil
}

func inClusterClient(caPath string) (*http.Client, error) {
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, errors.New("in-cluster CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("invalid in-cluster CA")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Timeout: 2 * time.Second, Transport: transport}, nil
}

func (m *Monitor) kubernetes(ctx context.Context, out *Snapshot) (map[string]replicaExpectation, bool) {
	config := m.config.Kubernetes
	observedAt := time.Now().UTC().Format(time.RFC3339Nano)
	out.Cluster = &Cluster{Status: "unavailable", Namespace: config.Namespace, ObservedAt: observedAt, Detail: "Kubernetes 状态采集不可用。", Nodes: []ClusterNode{}}
	indices := prepareKubernetesServices(out, observedAt)
	if config.initErr != nil || config.APIURL == "" || config.Namespace == "" {
		out.Source = "kubernetes_unavailable"
		markKubernetesUnavailable(out)
		return nil, true
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	observation := m.readKubernetesMetadata(ctx, client, config)
	observeKubernetesNodes(out.Cluster, observation.nodes, observation.nodesErr)
	if observation.unavailable() {
		out.Source = "kubernetes_unavailable"
		markKubernetesUnavailable(out)
		return nil, true
	}
	if observation.podsErr != nil {
		markKubernetesPodsUnknown(out)
	} else {
		observeKubernetesPods(out, observation.pods, indices, config.Namespace, observedAt)
	}
	out.Source = "kubernetes"
	if observation.partial() {
		out.Source = "kubernetes_partial"
	}
	return deploymentExpectations(observation.deployments, observation.deploymentsErr), observation.podsErr != nil
}

func prepareKubernetesServices(out *Snapshot, observedAt string) map[string]int {
	indices := make(map[string]int, len(out.Services))
	for i := range out.Services {
		indices[out.Services[i].ID] = i
		out.Services[i].LogicalID = out.Services[i].ID
		out.Services[i].ObservedAt = observedAt
		if !optional(out.Services[i].ID) {
			out.Services[i].Status = "unavailable"
			out.Services[i].Detail = "当前命名空间未发现预期服务 Pod。"
		}
	}
	return indices
}

type kubernetesObservation struct {
	nodes                             kubernetesNodeList
	pods                              kubernetesPodList
	deployments                       kubernetesDeploymentList
	nodesErr, podsErr, deploymentsErr error
}

func (o kubernetesObservation) unavailable() bool { return o.nodesErr != nil && o.podsErr != nil }
func (o kubernetesObservation) partial() bool {
	return o.nodesErr != nil || o.podsErr != nil || o.deploymentsErr != nil
}

// Independent reads own distinct result fields and are merged only after Wait.
func (m *Monitor) readKubernetesMetadata(ctx context.Context, client *http.Client, config *KubernetesConfig) kubernetesObservation {
	var result kubernetesObservation
	var requests errgroup.Group
	requests.SetLimit(3)
	requests.Go(func() error {
		result.nodesErr = m.kubernetesGet(ctx, client, config, "/api/v1/nodes", &result.nodes)
		return nil
	})
	requests.Go(func() error {
		path := "/api/v1/namespaces/" + url.PathEscape(config.Namespace) + "/pods"
		result.podsErr = m.kubernetesGet(ctx, client, config, path, &result.pods)
		return nil
	})
	requests.Go(func() error {
		path := "/apis/apps/v1/namespaces/" + url.PathEscape(config.Namespace) + "/deployments"
		result.deploymentsErr = m.kubernetesGet(ctx, client, config, path, &result.deployments)
		return nil
	})
	_ = requests.Wait()
	return result
}

func observeKubernetesNodes(cluster *Cluster, nodes kubernetesNodeList, err error) {
	if err != nil {
		cluster.Detail = "Kubernetes 节点观测不可用；服务 Pod 观测按可用结果呈现。"
		return
	}
	cluster.Nodes = make([]ClusterNode, 0, len(nodes.Items))
	readyNodes := 0
	for _, node := range nodes.Items {
		status, _ := kubernetesReadyCondition(node.Status.Conditions)
		states := map[string]string{"True": "ready", "False": "unavailable"}
		observed, ok := states[status]
		if !ok {
			observed = "unknown"
		}
		if observed == "ready" {
			readyNodes++
		}
		cluster.Nodes = append(cluster.Nodes, ClusterNode{Name: node.Metadata.Name, Status: observed, Version: node.Status.NodeInfo.KubeletVersion})
	}
	if len(nodes.Items) > 0 && readyNodes == len(nodes.Items) {
		cluster.Status = "ready"
		cluster.Detail = fmt.Sprintf("观察到 %d/%d 个 Ready Kubernetes 节点；节点数量不证明物理高可用。", readyNodes, len(nodes.Items))
	} else {
		cluster.Detail = fmt.Sprintf("观察到 %d/%d 个 Ready Kubernetes 节点；集群未完全就绪。", readyNodes, len(nodes.Items))
	}
}

func observeKubernetesPods(out *Snapshot, pods kubernetesPodList, indices map[string]int, fallbackNamespace, observedAt string) {
	seenPods := make(map[string]int)
	for _, pod := range pods.Items {
		name := pod.Metadata.Labels["app.kubernetes.io/name"]
		if name == "" {
			continue
		}
		index, expected := indices[name]
		count := seenPods[name]
		var service Service
		if expected {
			service = out.Services[index]
		} else {
			service = Service{ID: name, Name: name, Category: "application", Kind: "container", Status: "unknown", Detail: "Kubernetes Pod 已发现，尚未确认就绪状态。", DependsOn: []string{}}
		}
		if count > 0 {
			service.ID = name + "/" + pod.Metadata.Name
			service.Name = name + " / " + pod.Metadata.Name
		}
		instanceID := cmp.Or(pod.Metadata.UID, pod.Metadata.Name)
		namespace := cmp.Or(pod.Metadata.Namespace, fallbackNamespace)
		service.LogicalID = name
		service.InstanceID = instanceID
		service.Namespace = namespace
		service.NodeName = pod.Spec.NodeName
		service.Workload = podWorkload(pod, name)
		service.ObservedAt = observedAt
		service.Status, service.Detail = podReadiness(pod)
		container := podContainerMetadata(pod, service.Status)
		service.Container = &container
		service.Kind = "container"
		if count == 0 && expected {
			out.Services[index] = service
		} else {
			out.Services = append(out.Services, service)
		}
		seenPods[name] = count + 1
	}
}

func deploymentExpectations(deployments kubernetesDeploymentList, deploymentsErr error) map[string]replicaExpectation {
	expectations := make(map[string]replicaExpectation, len(deployments.Items))
	if deploymentsErr == nil {
		for _, deployment := range deployments.Items {
			logical := deployment.Metadata.Labels["app.kubernetes.io/name"]
			if logical == "" {
				logical = deployment.Metadata.Name
			}
			desired := 1
			if deployment.Spec.Replicas != nil {
				desired = *deployment.Spec.Replicas
			}
			expectations[logical] = replicaExpectation{desired: desired, source: "deployment"}
		}
	}
	return expectations
}

func markKubernetesPodsUnknown(out *Snapshot) {
	for i := range out.Services {
		if out.Services[i].Container == nil && !optional(out.Services[i].ID) {
			out.Services[i].Status = "unknown"
			out.Services[i].Detail = "Kubernetes Pod 观测不可用，无法确认服务是否存在。"
		}
	}
}

func markKubernetesUnavailable(out *Snapshot) {
	for i := range out.Services {
		if out.Services[i].Container != nil {
			out.Services[i].Status = "unknown"
			out.Services[i].Detail = "Kubernetes 状态采集不可用，无法确认 Pod 状态。"
		} else if !optional(out.Services[i].ID) {
			out.Services[i].Status = "unknown"
			out.Services[i].Detail = "Kubernetes 状态采集不可用，无法确认服务是否存在。"
		}
	}
}

func (m *Monitor) kubernetesGet(ctx context.Context, client *http.Client, config *KubernetesConfig, path string, result any) error {
	token := config.Token
	if config.tokenPath != "" {
		var err error
		token, err = readKubernetesToken(config.tokenPath)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.APIURL, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Kubernetes metadata unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, kubernetesBodyLimit+1))
	if err != nil || len(data) > kubernetesBodyLimit {
		return errors.New("Kubernetes metadata unavailable")
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("Kubernetes metadata unavailable")
	}
	return nil
}

type kubernetesNodeList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
			} `json:"nodeInfo"`
			Conditions []kubernetesCondition `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type kubernetesPodList struct {
	Items []kubernetesPod `json:"items"`
}

type kubernetesDeploymentList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas int `json:"readyReplicas"`
		} `json:"status"`
	} `json:"items"`
}

type kubernetesPod struct {
	Metadata struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		UID             string            `json:"uid"`
		Labels          map[string]string `json:"labels"`
		OwnerReferences []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Name string `json:"name"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string                      `json:"phase"`
		Conditions        []kubernetesCondition       `json:"conditions"`
		ContainerStatuses []kubernetesContainerStatus `json:"containerStatuses"`
	} `json:"status"`
}

func podReadiness(pod kubernetesPod) (string, string) {
	phases := map[string]observationState{
		"Pending":   {"starting", "Kubernetes Pod 正在等待调度或启动。"},
		"Failed":    {"unavailable", "Kubernetes Pod 运行失败。"},
		"Succeeded": {"completed", "Kubernetes Pod 已成功完成。"},
		"Unknown":   {"unknown", "Kubernetes Pod 状态未知。"},
	}
	if state, ok := phases[pod.Status.Phase]; ok {
		return state.status, state.detail
	}
	condition, known := kubernetesReadyCondition(pod.Status.Conditions)
	if !known {
		return "unknown", "Kubernetes Pod 就绪或容器状态信息不完整。"
	}
	if condition != "True" {
		return "unavailable", "Kubernetes Pod Ready 条件未满足。"
	}
	if status, detail := podContainersReadiness(pod); status != "" {
		return status, detail
	}
	if pod.Status.Phase == "Running" {
		return "ready", "Kubernetes Pod 正在运行且所有容器已就绪。"
	}
	return "unknown", "Kubernetes Pod 就绪状态未知。"
}

func podContainersReadiness(pod kubernetesPod) (string, string) {
	if len(pod.Spec.Containers) == 0 || len(pod.Status.ContainerStatuses) != len(pod.Spec.Containers) {
		return "unknown", "Kubernetes Pod 就绪或容器状态信息不完整。"
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.State.Waiting != nil || container.State.Terminated != nil {
			return "unavailable", "Kubernetes Pod 存在未就绪或已终止容器。"
		}
		if container.State.Running == nil || !container.Ready {
			return "unavailable", "Kubernetes Pod 容器尚未就绪。"
		}
	}
	return "", ""
}

func podContainerMetadata(pod kubernetesPod, readiness string) Container {
	healthStates := map[string]string{"ready": "healthy", "starting": "starting", "unavailable": "unhealthy", "completed": "completed"}
	item := Container{ID: cmp.Or(pod.Metadata.UID, pod.Metadata.Name), Name: pod.Metadata.Name,
		State: pod.Status.Phase, Health: cmp.Or(healthStates[readiness], "unknown")}
	for _, container := range pod.Status.ContainerStatuses {
		item.Restarts += container.RestartCount
		item.Image = cmp.Or(item.Image, container.Image)
		applyPodContainerState(&item, container)
	}
	return item
}

func applyPodContainerState(item *Container, container kubernetesContainerStatus) {
	if container.State.Running != nil {
		item.StartedAt = container.State.Running.StartedAt
		item.State = "running"
	} else if container.State.Waiting != nil {
		item.State = "waiting"
	} else if container.State.Terminated != nil {
		item.State = "terminated"
	}
}

func podWorkload(pod kubernetesPod, fallback string) string {
	for _, owner := range pod.Metadata.OwnerReferences {
		switch owner.Kind {
		case "StatefulSet", "DaemonSet", "Job":
			return owner.Name
		}
	}
	return fallback
}

type kubernetesCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

func kubernetesReadyCondition(conditions []kubernetesCondition) (string, bool) {
	for _, condition := range conditions {
		if condition.Type == "Ready" {
			return condition.Status, true
		}
	}
	return "", false
}

type kubernetesContainerStatus struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Running *struct {
			StartedAt string `json:"startedAt"`
		} `json:"running"`
		Waiting *struct {
			Reason string `json:"reason"`
		} `json:"waiting"`
		Terminated *struct {
			Reason string `json:"reason"`
		} `json:"terminated"`
	} `json:"state"`
}
