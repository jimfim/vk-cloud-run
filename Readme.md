# Virtual Kubelet  - Cloud run
A virtual kubelet implementation for Google CloudRun

## Kubernetes Virtual Kubelet with Cloud Run
The Google Cloud Run provider for the Virtual Kubelet configures Google Cloud Run service as a virtual node in a Kubernetes cluster. Hence, pods scheduled on the virtual node can be as Google Cloud Run services. This configuration allows users to take advantage of both the capabilities of Kubernetes and the management value and cost benefit of Cloud Run.

## Features 

Virtual Kubelet's Google Cloud Run provider relies heavily on the feature set that Cloud Run service provides. 

### Supported
* Environment Variables
* More to come

### Limitations (Not supported)
* Anything enforced by Cloud Run
* * [strict resource settings](https://cloud.google.com/run/docs/configuring/services/cpu)
* * 1 ingress 
* 

## Identity

### (Recommended) IAM Workflow Identity

### Credentials

Follow the guide here to download an a file with your credentials https://cloud.google.com/docs/authentication/application-default-credentials
and install this as a config map in your local Kubernetes instance to grant the virtual kubelet access to your Google Cloud run instance
```
kubectl create configmap application-default-credentials --from-file=./application_default_credentials.json
```

## Installation

### Helm

```bash
helm repo add vk-cloud-run https://jimfim.github.io/vk-cloud-run
```

```bash
helm install vk-cloud-run jimfim/vk-cloud-run
```

### Configuration

#### Node
there is file here that  will allow you to configure the specifications of the Cloud run provider node instance
https://github.com/jimfim/vk-cloud-run/blob/main/src/vkubelet-cfg.json

#### Cloud Run
In writing this i relaize my region and project id are hard coded directly into the kubelet... whoops
I should fix this https://github.com/jimfim/vk-cloud-run/issues/5

### Local Docker Desktop
In root directory.

Build local image

``` Bash
docker build -t localhost:5000/virtual-kubelet -f .\Containerfile .
```
Install Virtual Kubelet and Demo app

```bash
kubectl apply -f .\charts\virtual-kubelet.yaml
kubectl apply -f .\charts\deploy.yaml
```
When you are finished, remove install

```bash
kubectl delete -f .\charts\virtual-kubelet.yaml
kubectl delete -f .\charts\deploy.yaml
```
### GKE
Helm install

```bash
helm install <coming soon>
```
Add a Toleration to your Deployment
```yaml
      tolerations:
      - key: virtual-kubelet.io/provider
        operator: Exists
```

# Quirks
## Resources 
https://cloud.google.com/run/docs/configuring/services/cpu
