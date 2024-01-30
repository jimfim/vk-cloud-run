package cloudrun

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/logging/logadmin"
	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	v1 "k8s.io/api/core/v1"
)

type TrackerConfig struct {
	region    string
	projectId string
}

type Tracker interface {
	Initialize(config TrackerConfig)
	ListPods() string
	GetPods() (*runpb.Service, error)
	DeletePod()
	CreateJob() (*run.CreateServiceOperation, error)
}

type ClientManager struct {
	config  TrackerConfig
	client  *run.ServicesClient
	name    string
	context context.Context
}

func (h *ClientManager) Initialize(config TrackerConfig) *run.ServicesClient {
	ctx := context.Background()
	serviceClient, err := run.NewServicesClient(ctx, option.WithCredentialsFile("/application_default_credentials.json"))
	if err != nil {
		fmt.Print(err)
	}
	h.client = serviceClient
	h.config = config
	h.context = ctx
	return serviceClient
}

func (h *ClientManager) ListPods() {
	parent := fmt.Sprintf("projects/%s/locations/%s", h.config.projectId, h.config.region)

	req := &runpb.ListServicesRequest{
		Parent:      parent,
		PageSize:    0,
		PageToken:   "",
		ShowDeleted: false,
	}

	it := h.client.ListServices(h.context, req)
	for {
		res, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			fmt.Println(err)
			break
		}
		fmt.Println(res)
	}
}

func (h *ClientManager) GetService(name string) runpb.Service {
	parent := fmt.Sprintf("projects/%s/locations/%s/services/%s", h.config.projectId, h.config.region, name)
	req := &runpb.GetServiceRequest{
		Name: parent,
	}

	op, err := h.client.GetService(h.context, req)
	if err != nil {
		fmt.Println(err)
	}

	return *op
}

func (h *ClientManager) DeleteService(name string) {
	parent := fmt.Sprintf("projects/%s/locations/%s/services/%s", h.config.projectId, h.config.region, name)
	req := &runpb.DeleteServiceRequest{
		Name:         parent,
		ValidateOnly: false,
	}

	op, err := h.client.DeleteService(h.context, req)
	if err != nil {
		fmt.Println(err)
		return
	}
	resp, err := op.Wait(h.context)
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = resp
}

func (h *ClientManager) CreateService(pod *v1.Pod) {
	parent := fmt.Sprintf("projects/%s/locations/%s", h.config.projectId, h.config.region)
	labels := map[string]string{
		"managed-by": "virtual-kubelet",
	}
	serviceContainers := make([]*runpb.Container, 0, len(pod.Spec.Containers))
	for _, containerSpec := range pod.Spec.Containers {

		portList := make([]*runpb.ContainerPort, 0, len(containerSpec.Ports))
		for i := range containerSpec.Ports {
			portList = append(portList, &runpb.ContainerPort{
				ContainerPort: containerSpec.Ports[i].ContainerPort,
				Name:          containerSpec.Ports[i].Name,
			})
		}

		resource := &runpb.ResourceRequirements{
			Limits: map[string]string{
				"cpu":    fmt.Sprintf("%d", containerSpec.Resources.Limits.Cpu().Value()),
				"memory": fmt.Sprintf("%dMi", containerSpec.Resources.Limits.Memory().Value()/1024/1024),
			},
		}

		envvars := make([]*runpb.EnvVar, 0, len(containerSpec.Env))
		for i := range containerSpec.Env {
			envvars = append(envvars, &runpb.EnvVar{
				Name: containerSpec.Env[i].Name,
				Values: &runpb.EnvVar_Value{
					Value: containerSpec.Env[i].Value,
				},
			})
		}

		container := &runpb.Container{
			Image:     containerSpec.Image,
			Ports:     portList,
			Resources: resource,
			Env:       envvars,
		}

		serviceContainers = append(serviceContainers, container)
	}

	req := &runpb.CreateServiceRequest{
		Parent: parent,
		Service: &runpb.Service{
			Labels:      labels,
			Description: "my-service",
			Template: &runpb.RevisionTemplate{
				Labels:     labels,
				Containers: serviceContainers,
			},
		},
		ServiceId:    pod.Name,
		ValidateOnly: false,
	}

	op, err := h.client.CreateService(h.context, req)
	if err != nil {
		fmt.Println(err)
	}

	resp, err := op.Wait(h.context)
	if err != nil {
		fmt.Println(err)
	}
	_ = resp
}

func (h *ClientManager) GetContainerLogs(name string) *strings.Reader {
	//parent := fmt.Sprintf("projects/%s/locations/%s/services/%s", h.config.projectId, h.config.region, name)
	adminClient, err := logadmin.NewClient(h.context, h.config.projectId, option.WithCredentialsFile("/application_default_credentials.json"))
	if err != nil {
		fmt.Println("Failed to create logadmin client: %v", err)
	}
	defer adminClient.Close()

	var buffer bytes.Buffer
	//lastHour := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)

	iter := adminClient.Entries(h.context,
		// Only get entries from the "log-example" log within the last hour.
		logadmin.Filter(fmt.Sprintf(`resource.labels.service_name = "%s"`, name)),
		// Get most recent entries first.
		//logadmin.NewestFirst(),
	)

	//fmt.Println("Failed to create logadmin client: %v", err)

	for {
		entry, err := iter.Next()
		if err == iterator.Done {
			return strings.NewReader(buffer.String())
		}
		if err != nil {
			return nil
		}
		buffer.WriteString(fmt.Sprintf("%b", entry.Payload))
	}
}
