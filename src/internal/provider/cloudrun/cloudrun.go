package cloudrun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"k8s.io/apimachinery/pkg/runtime"

	run "cloud.google.com/go/run/apiv2"
	dto "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	stats "github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"google.golang.org/api/option"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Provider configuration defaults.
	defaultCPUCapacity    = "20"
	defaultMemoryCapacity = "100Gi"
	defaultPodCapacity    = "20"

	// Values used in tracing as attribute keys.
	namespaceKey     = "namespace"
	nameKey          = "name"
	containerNameKey = "containerName"
)

// See: https://github.com/virtual-kubelet/virtual-kubelet/issues/632
/*
var (
	_ providers.Provider           = (*MockV0Provider)(nil)
	_ providers.PodMetricsProvider = (*MockV0Provider)(nil)
	_ node.PodNotifier         = (*CloudRunProvider)(nil)
)
*/

// CloudRunProvider implements the virtual-kubelet provider interface and stores pods in memory.
type CloudRunProvider struct { //nolint:golint
	nodeName           string
	operatingSystem    string
	internalIP         string
	daemonEndpointPort int32
	pods               map[string]*v1.Pod
	config             CloudRunConfig
	startTime          time.Time
	notifier           func(*v1.Pod)
	crclient           ClientManager
	tracker            *PodsTracker
	pconfig            nodeutil.ProviderConfig
}

func (p *CloudRunProvider) ListActivePods(ctx context.Context) ([]PodIdentifier, error) {
	ctx, span := trace.StartSpan(ctx, "CloudRunProvider.ListActivePods")
	defer span.End()

	providerPods, err := p.GetPods(ctx)
	if err != nil {
		return nil, err
	}
	podsIdentifiers := make([]PodIdentifier, 0, len(providerPods))

	for _, pod := range providerPods {
		podsIdentifiers = append(
			podsIdentifiers,
			PodIdentifier{
				namespace: pod.Namespace,
				name:      pod.Name,
			})
	}

	return podsIdentifiers, nil
}

func (p *CloudRunProvider) FetchPodStatus(ctx context.Context, ns, name string) (*v1.PodStatus, error) {
	ctx, span := trace.StartSpan(ctx, "CloudRunProvider.FetchPodStatus")
	defer span.End()

	return p.GetPodStatus(ctx, ns, name)
}

func (p *CloudRunProvider) FetchPodEvents(ctx context.Context, pod *v1.Pod, evtSink func(timestamp *time.Time, object runtime.Object, eventtype string, reason string, messageFmt string, args ...interface{})) error {
	ctx, span := trace.StartSpan(ctx, "CloudRunProvider.FetchPodEvents")
	defer span.End()
	return nil
}

func (p *CloudRunProvider) CleanupPod(ctx context.Context, ns, name string) error {
	ctx, span := trace.StartSpan(ctx, "CloudRunProvider.CleanupPod")
	defer span.End()
	p.crclient.DeleteService(name)
	return nil
}

// CloudRunConfig contains a mock virtual-kubelet's configurable parameters.
type CloudRunConfig struct { //nolint:golint
	Region     string
	ProjectId  string
	CPU        string            `json:"cpu,omitempty"`
	Memory     string            `json:"memory,omitempty"`
	Pods       string            `json:"pods,omitempty"`
	Others     map[string]string `json:"others,omitempty"`
	ProviderID string            `json:"providerID,omitempty"`
}

// NewCloudRunProvider creates a new MockV0Provider. Mock legacy provider does not implement the new asynchronous podnotifier interface
func NewCloudRunProviderConfig(config CloudRunConfig, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32, providerConfig nodeutil.ProviderConfig) (*CloudRunProvider, error) {
	// set defaults
	if config.CPU == "" {
		config.CPU = defaultCPUCapacity
	}
	if config.Memory == "" {
		config.Memory = defaultMemoryCapacity
	}
	if config.Pods == "" {
		config.Pods = defaultPodCapacity
	}
	client := ClientManager{}
	crconfgi := TrackerConfig{
		region:    os.Getenv("region"),
		projectId: os.Getenv("projectId"),
	}

	client.Initialize(crconfgi)

	provider := CloudRunProvider{
		nodeName:           nodeName,
		operatingSystem:    operatingSystem,
		internalIP:         internalIP,
		daemonEndpointPort: daemonEndpointPort,
		pods:               make(map[string]*v1.Pod),
		config:             config,
		startTime:          time.Now(),
		crclient:           client,
		pconfig:            providerConfig,
	}
	return &provider, nil
}

// NewCloudRunProvider creates a new CloudRunProvider, which implements the PodNotifier interface
func NewCloudRunProvider(providerConfig, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32, pconfig nodeutil.ProviderConfig) (*CloudRunProvider, error) {
	config, err := loadConfig(providerConfig, nodeName)
	if err != nil {
		return nil, err
	}

	return NewCloudRunProviderConfig(config, nodeName, operatingSystem, internalIP, daemonEndpointPort, pconfig)
}

// loadConfig loads the given json configuration files.
func loadConfig(providerConfig, nodeName string) (config CloudRunConfig, err error) {
	data, err := os.ReadFile(providerConfig)
	if err != nil {
		return config, err
	}
	configMap := map[string]CloudRunConfig{}
	err = json.Unmarshal(data, &configMap)
	if err != nil {
		return config, err
	}
	if _, exist := configMap[nodeName]; exist {
		config = configMap[nodeName]
		if config.CPU == "" {
			config.CPU = defaultCPUCapacity
		}
		if config.Memory == "" {
			config.Memory = defaultMemoryCapacity
		}
		if config.Pods == "" {
			config.Pods = defaultPodCapacity
		}
	}

	if _, err = resource.ParseQuantity(config.CPU); err != nil {
		return config, fmt.Errorf("Invalid CPU value %v", config.CPU)
	}
	if _, err = resource.ParseQuantity(config.Memory); err != nil {
		return config, fmt.Errorf("Invalid memory value %v", config.Memory)
	}
	if _, err = resource.ParseQuantity(config.Pods); err != nil {
		return config, fmt.Errorf("Invalid pods value %v", config.Pods)
	}
	for _, v := range config.Others {
		if _, err = resource.ParseQuantity(v); err != nil {
			return config, fmt.Errorf("Invalid other value %v", v)
		}
	}
	return config, nil
}

// CreatePod accepts a Pod definition and stores it in memory.
func (p *CloudRunProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {

	ctx, span := trace.StartSpan(ctx, "CreatePod")
	defer span.End()

	c, err := run.NewServicesClient(ctx, option.WithCredentialsFile("/application_default_credentials.json"))
	defer c.Close()
	if err != nil {
		fmt.Println(err)
		return err
	}

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	log.G(ctx).Infof("receive CreatePod %q", pod.Name)
	p.crclient.CreateService(pod)
	now := metav1.NewTime(time.Now())
	pod.Status = v1.PodStatus{
		Phase:     v1.PodRunning,
		HostIP:    "1.2.3.4",
		PodIP:     "5.6.7.8",
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:   v1.PodInitialized,
				Status: v1.ConditionTrue,
			},
			{
				Type:   v1.PodReady,
				Status: v1.ConditionTrue,
			},
			{
				Type:   v1.PodScheduled,
				Status: v1.ConditionTrue,
			},
		},
	}
	for _, container := range pod.Spec.Containers {
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, v1.ContainerStatus{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        true,
			RestartCount: 0,
			State: v1.ContainerState{
				Running: &v1.ContainerStateRunning{
					StartedAt: now,
				},
			},
		})
	}
	log.G(ctx).Infof("printing pod %q", pod)
	//p.notifier(pod)
	return nil
}

// UpdatePod accepts a Pod definition and updates its reference.
func (p *CloudRunProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)

	log.G(ctx).Infof("receive UpdatePod %q", pod.Name)

	key, err := buildKey(pod)
	if err != nil {
		return err
	}

	p.pods[key] = pod
	//p.notifier(pod)

	return nil
}

// DeletePod deletes the specified pod out of memory.
func (p *CloudRunProvider) DeletePod(ctx context.Context, pod *v1.Pod) (err error) {
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	ctx = addAttributes(ctx, span, namespaceKey, pod.Namespace, nameKey, pod.Name)
	p.crclient.DeleteService(pod.Name)
	now := metav1.Now()
	pod.Status.Phase = v1.PodSucceeded
	pod.Status.Reason = "CloudRunProviderPodDeleted"

	for idx := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[idx].Ready = false
		pod.Status.ContainerStatuses[idx].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				Message:    "CloudRun provider terminated container upon deletion",
				FinishedAt: now,
				Reason:     "CloudRunProviderPodContainerDeleted",
				//StartedAt:  pod.Status.ContainerStatuses[idx].State.Running.StartedAt,
			},
		}
	}

	//p.notifierp.notifier(pod)
	return nil
}

// GetPod returns a pod by name that is stored in memory.
func (p *CloudRunProvider) GetPod(ctx context.Context, namespace, name string) (pod *v1.Pod, err error) {
	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer func() {
		span.SetStatus(err)
		span.End()
	}()

	// Add the pod's coordinates to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, name)

	log.G(ctx).Infof("receive GetPod %q", name)

	key, err := buildKeyFromNames(namespace, name)
	if err != nil {
		return nil, err
	}

	if pod, ok := p.pods[key]; ok {
		return pod, nil
	}
	return nil, errdefs.NotFoundf("pod \"%s/%s\" is not known to the provider", namespace, name)
}

// GetContainerLogs retrieves the logs of a container by name from the provider.
func (p *CloudRunProvider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	ctx, span := trace.StartSpan(ctx, "GetContainerLogs")
	defer span.End()

	// Add pod and container attributes to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, podName, containerNameKey, containerName)
	log.G(ctx).Infof("receive GetContainerLogs %q", podName)
	logs := p.crclient.GetContainerLogs(podName)
	return io.NopCloser(logs), nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *CloudRunProvider) RunInContainer(ctx context.Context, namespace, name, container string, cmd []string, attach api.AttachIO) error {
	log.G(context.TODO()).Infof("receive ExecInContainer %q", container)
	return nil
}

// AttachToContainer attaches to the executing process of a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *CloudRunProvider) AttachToContainer(ctx context.Context, namespace, name, container string, attach api.AttachIO) error {
	log.G(ctx).Infof("receive AttachToContainer %q", container)
	return nil
}

// PortForward forwards a local port to a port on the pod
func (p *CloudRunProvider) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	log.G(ctx).Infof("receive PortForward %q", pod)
	return nil
}

// GetPodStatus returns the status of a pod by name that is "running".
// returns nil if a pod by that name is not found.
func (p *CloudRunProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {
	ctx, span := trace.StartSpan(ctx, "GetPodStatus")
	defer span.End()
	// Add namespace and name as attributes to the current span.
	ctx = addAttributes(ctx, span, namespaceKey, namespace, nameKey, name)
	log.G(ctx).Infof("receive GetPodStatus %q", name)

	allReady := true
	var firstContainerStartTime, lastUpdateTime time.Time
	livePod := p.crclient.GetService(name)
	//livePod
	containersList := livePod.Template.Containers
	containerStatuses := make([]v1.ContainerStatus, 0, len(livePod.Template.Containers))
	lastUpdateTime = livePod.UpdateTime.AsTime()
	firstContainerStartTime = livePod.CreateTime.AsTime()

	for i := range containersList {
		container := containersList[i]
		//containerState := container
		containerStatus := v1.ContainerStatus{
			Name: container.Name,
			//State: containerState,
			//LastTerminationState: container.Properties.InstanceView.PreviousState,
			Ready:        true,
			Started:      &allReady,
			RestartCount: 0,
			Image:        container.Image,
			ImageID:      container.GetImage(),
			//ContainerID:  container.GetImage(),
		}
		containerStatuses = append(containerStatuses, containerStatus)
	}

	podIp := ""
	return &v1.PodStatus{
		Phase:             getPodPhaseFromCloudRunState(livePod.TerminalCondition.State.String()),
		Conditions:        getPodConditionsFromCloudRunState(livePod.TerminalCondition.State.String(), firstContainerStartTime, lastUpdateTime, allReady),
		Message:           "",
		Reason:            "",
		HostIP:            p.internalIP,
		PodIP:             podIp,
		StartTime:         &metav1.Time{Time: firstContainerStartTime},
		ContainerStatuses: containerStatuses,
	}, nil
}

// GetPods returns a list of all pods known to be "running".
func (p *CloudRunProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	log.G(ctx).Info("receive GetPods")

	var pods []*v1.Pod

	for _, pod := range p.pods {
		pods = append(pods, pod)
	}

	return pods, nil
}

func (p *CloudRunProvider) ConfigureNode(ctx context.Context, n *v1.Node) { //nolint:golint
	ctx, span := trace.StartSpan(ctx, "mock.ConfigureNode") //nolint:staticcheck,ineffassign
	defer span.End()

	if p.config.ProviderID != "" {
		n.Spec.ProviderID = p.config.ProviderID
	}
	n.Status.Capacity = p.capacity()
	n.Status.Allocatable = p.capacity()
	n.Status.Conditions = p.nodeConditions()
	n.Status.Addresses = p.nodeAddresses()
	n.Status.DaemonEndpoints = p.nodeDaemonEndpoints()
	os := p.operatingSystem
	if os == "" {
		os = "linux"
	}
	n.Status.NodeInfo.OperatingSystem = os
	n.Status.NodeInfo.Architecture = "amd64"
	n.ObjectMeta.Labels["alpha.service-controller.kubernetes.io/exclude-balancer"] = "true"
	n.ObjectMeta.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"
}

// Capacity returns a resource list containing the capacity limits.
func (p *CloudRunProvider) capacity() v1.ResourceList {
	rl := v1.ResourceList{
		"cpu":    resource.MustParse(p.config.CPU),
		"memory": resource.MustParse(p.config.Memory),
		"pods":   resource.MustParse(p.config.Pods),
	}
	for k, v := range p.config.Others {
		rl[v1.ResourceName(k)] = resource.MustParse(v)
	}
	return rl
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *CloudRunProvider) nodeConditions() []v1.NodeCondition {
	// TODO: Make this configurable
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletPending",
			Message:            "kubelet is pending.",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

}

// NodeAddresses returns a list of addresses for the node status
// within Kubernetes.
func (p *CloudRunProvider) nodeAddresses() []v1.NodeAddress {
	return []v1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: p.internalIP,
		},
	}
}

// NodeDaemonEndpoints returns NodeDaemonEndpoints for the node status
// within Kubernetes.
func (p *CloudRunProvider) nodeDaemonEndpoints() v1.NodeDaemonEndpoints {
	return v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}

// GetStatsSummary returns dummy stats for all pods known by this provider.
func (p *CloudRunProvider) GetStatsSummary(ctx context.Context) (*stats.Summary, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetStatsSummary") //nolint: ineffassign,staticcheck
	defer span.End()

	// Grab the current timestamp so we can report it as the time the stats were generated.
	time := metav1.NewTime(time.Now())

	// Create the Summary object that will later be populated with node and pod stats.
	res := &stats.Summary{}

	// Populate the Summary object with basic node stats.
	res.Node = stats.NodeStats{
		NodeName:  p.nodeName,
		StartTime: metav1.NewTime(p.startTime),
	}

	// Populate the Summary object with dummy stats for each pod known by this provider.
	for _, pod := range p.pods {
		var (
			// totalUsageNanoCores will be populated with the sum of the values of UsageNanoCores computes across all containers in the pod.
			totalUsageNanoCores uint64
			// totalUsageBytes will be populated with the sum of the values of UsageBytes computed across all containers in the pod.
			totalUsageBytes uint64
		)

		// Create a PodStats object to populate with pod stats.
		pss := stats.PodStats{
			PodRef: stats.PodReference{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				UID:       string(pod.UID),
			},
			StartTime: pod.CreationTimestamp,
		}

		// Iterate over all containers in the current pod to compute dummy stats.
		for _, container := range pod.Spec.Containers {
			// Grab a dummy value to be used as the total CPU usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.

			/* #nosec */
			dummyUsageNanoCores := uint64(rand.Uint32())
			totalUsageNanoCores += dummyUsageNanoCores
			// Create a dummy value to be used as the total RAM usage.
			// The value should fit a uint32 in order to avoid overflows later on when computing pod stats.

			/* #nosec */
			dummyUsageBytes := uint64(rand.Uint32())
			totalUsageBytes += dummyUsageBytes
			// Append a ContainerStats object containing the dummy stats to the PodStats object.
			pss.Containers = append(pss.Containers, stats.ContainerStats{
				Name:      container.Name,
				StartTime: pod.CreationTimestamp,
				CPU: &stats.CPUStats{
					Time:           time,
					UsageNanoCores: &dummyUsageNanoCores,
				},
				Memory: &stats.MemoryStats{
					Time:       time,
					UsageBytes: &dummyUsageBytes,
				},
			})
		}

		// Populate the CPU and RAM stats for the pod and append the PodsStats object to the Summary object to be returned.
		pss.CPU = &stats.CPUStats{
			Time:           time,
			UsageNanoCores: &totalUsageNanoCores,
		}
		pss.Memory = &stats.MemoryStats{
			Time:       time,
			UsageBytes: &totalUsageBytes,
		}
		res.Pods = append(res.Pods, pss)
	}

	// Return the dummy stats.
	return res, nil
}

func (p *CloudRunProvider) generateMockMetrics(metricsMap map[string][]*dto.Metric, resourceType string, label []*dto.LabelPair) map[string][]*dto.Metric {
	var (
		cpuMetricSuffix    = "_cpu_usage_seconds_total"
		memoryMetricSuffix = "_memory_working_set_bytes"
		dummyValue         = float64(100)
	)

	if metricsMap == nil {
		metricsMap = map[string][]*dto.Metric{}
	}

	finalCpuMetricName := resourceType + cpuMetricSuffix
	finalMemoryMetricName := resourceType + memoryMetricSuffix

	newCPUMetric := dto.Metric{
		Label: label,
		Counter: &dto.Counter{
			Value: &dummyValue,
		},
	}
	newMemoryMetric := dto.Metric{
		Label: label,
		Gauge: &dto.Gauge{
			Value: &dummyValue,
		},
	}
	// if metric family exists add to metric array
	if cpuMetrics, ok := metricsMap[finalCpuMetricName]; ok {
		metricsMap[finalCpuMetricName] = append(cpuMetrics, &newCPUMetric)
	} else {
		metricsMap[finalCpuMetricName] = []*dto.Metric{&newCPUMetric}
	}
	if memoryMetrics, ok := metricsMap[finalMemoryMetricName]; ok {
		metricsMap[finalMemoryMetricName] = append(memoryMetrics, &newMemoryMetric)
	} else {
		metricsMap[finalMemoryMetricName] = []*dto.Metric{&newMemoryMetric}
	}

	return metricsMap
}

func (p *CloudRunProvider) getMetricType(metricName string) *dto.MetricType {
	var (
		dtoCounterMetricType = dto.MetricType_COUNTER
		dtoGaugeMetricType   = dto.MetricType_GAUGE
		cpuMetricSuffix      = "_cpu_usage_seconds_total"
		memoryMetricSuffix   = "_memory_working_set_bytes"
	)
	if strings.HasSuffix(metricName, cpuMetricSuffix) {
		return &dtoCounterMetricType
	}
	if strings.HasSuffix(metricName, memoryMetricSuffix) {
		return &dtoGaugeMetricType
	}

	return nil
}

func (p *CloudRunProvider) GetMetricsResource(ctx context.Context) ([]*dto.MetricFamily, error) {
	var span trace.Span
	ctx, span = trace.StartSpan(ctx, "GetMetricsResource") //nolint: ineffassign,staticcheck
	defer span.End()

	var (
		nodeNameStr      = "NodeName"
		podNameStr       = "PodName"
		containerNameStr = "containerName"
	)
	nodeLabels := []*dto.LabelPair{
		{
			Name:  &nodeNameStr,
			Value: &p.nodeName,
		},
	}

	metricsMap := p.generateMockMetrics(nil, "node", nodeLabels)
	for _, pod := range p.pods {
		podLabels := []*dto.LabelPair{
			{
				Name:  &nodeNameStr,
				Value: &p.nodeName,
			},
			{
				Name:  &podNameStr,
				Value: &pod.Name,
			},
		}
		metricsMap = p.generateMockMetrics(metricsMap, "pod", podLabels)
		for _, container := range pod.Spec.Containers {
			containerLabels := []*dto.LabelPair{
				{
					Name:  &nodeNameStr,
					Value: &p.nodeName,
				},
				{
					Name:  &podNameStr,
					Value: &pod.Name,
				},
				{
					Name:  &containerNameStr,
					Value: &container.Name,
				},
			}
			metricsMap = p.generateMockMetrics(metricsMap, "container", containerLabels)
		}
	}

	res := []*dto.MetricFamily{}
	for metricName := range metricsMap {
		tempName := metricName
		tempMetrics := metricsMap[tempName]

		metricFamily := dto.MetricFamily{
			Name:   &tempName,
			Type:   p.getMetricType(tempName),
			Metric: tempMetrics,
		}
		res = append(res, &metricFamily)
	}

	return res, nil
}

// NotifyPods is called to set a pod notifier callback function. This should be called before any operations are done
// within the provider.
func (p *CloudRunProvider) NotifyPods(ctx context.Context, notifier func(*v1.Pod)) {
	ctx, span := trace.StartSpan(ctx, "ACIProvider.NotifyPods")
	defer span.End()

	// Capture the notifier to be used for communicating updates to VK
	p.tracker = &PodsTracker{
		updateCb:       notifier,
		handler:        p,
		pods:           p.pconfig.Pods,
		lastEventCheck: time.UnixMicro(0),
	}

	go p.tracker.StartTracking(ctx)
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz1234567890")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func buildKeyFromNames(namespace string, name string) (string, error) {
	return fmt.Sprintf("%s-%s-%s", namespace, name, RandStringRunes(6)), nil
}

// buildKey is a helper for building the "key" for the providers pod store.
func buildKey(pod *v1.Pod) (string, error) {
	if pod.ObjectMeta.Namespace == "" {
		return "", fmt.Errorf("pod namespace not found")
	}

	if pod.ObjectMeta.Name == "" {
		return "", fmt.Errorf("pod name not found")
	}

	return buildKeyFromNames(pod.ObjectMeta.Namespace, pod.ObjectMeta.Name)
}

// addAttributes adds the specified attributes to the provided span.
// attrs must be an even-sized list of string arguments.
// Otherwise, the span won't be modified.
// TODO: Refactor and move to a "tracing utilities" package.
func addAttributes(ctx context.Context, span trace.Span, attrs ...string) context.Context {
	if len(attrs)%2 == 1 {
		return ctx
	}
	for i := 0; i < len(attrs); i += 2 {
		ctx = span.WithField(ctx, attrs[i], attrs[i+1])
	}
	return ctx
}

func getPodConditionsFromCloudRunState(state string, creationTime, lastUpdateTime time.Time, allReady bool) []v1.PodCondition {
	// cg state is validated
	switch state {
	case "Running", "CONDITION_SUCCEEDED":
		readyConditionStatus := v1.ConditionFalse
		readyConditionTime := creationTime
		if allReady {
			readyConditionStatus = v1.ConditionTrue
			readyConditionTime = lastUpdateTime
		}

		return []v1.PodCondition{
			{
				Type:               v1.PodReady,
				Status:             readyConditionStatus,
				LastTransitionTime: metav1.Time{Time: readyConditionTime},
			}, {
				Type:               v1.PodInitialized,
				Status:             v1.ConditionTrue,
				LastTransitionTime: metav1.Time{Time: creationTime},
			}, {
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: metav1.Time{Time: creationTime},
			},
		}
	}
	return []v1.PodCondition{}
}
func getPodPhaseFromCloudRunState(state string) v1.PodPhase {
	switch state {
	case "CONDITION_SUCCEEDED":
		return v1.PodRunning
	case "CONDITION_FAILED":
		return v1.PodFailed
	case "CONDITION_RECONCILING":
		return v1.PodPending
	case "CONDITION_PENDING":
		return v1.PodPending
	}

	return v1.PodRunning
}
