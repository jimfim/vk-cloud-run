package cloudrun

import (
	"context"
	"fmt"

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

func (h *ClientManager) DeletePod(name string) {
	parent := fmt.Sprintf("projects/%s/locations/%s/services/%s", h.config.projectId, h.config.region, name)
	req := &runpb.DeleteServiceRequest{
		Name:         parent,
		ValidateOnly: false,
	}

	op, err := h.client.DeleteService(h.context, req)
	if err != nil {
		fmt.Println(err)
	}
	resp, err := op.Wait(h.context)
	if err != nil {
		fmt.Println(err)
	}
	_ = resp
}

func (h *ClientManager) CreatePod(pod *v1.Pod) {
	parent := fmt.Sprintf("projects/%s/locations/%s", h.config.projectId, h.config.region)
	labels := map[string]string{
		"managed-by": "virtual-kubelet",
	}

	req := &runpb.CreateServiceRequest{
		Parent: parent,
		Service: &runpb.Service{
			Labels:      labels,
			Description: "my-service",
			Template: &runpb.RevisionTemplate{
				Labels: labels,
				Containers: []*runpb.Container{
					{
						Image: "us-docker.pkg.dev/cloudrun/container/hello",
						Ports: []*runpb.ContainerPort{{ContainerPort: 8080}},
					},
				},
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
