# Virtual Kubelet  - Cloud run
A virtual kubelet implementation for cloud run

## Installing

## Building

# Build Container
docker build -t localhost:5000/virtual-kubelet -f .\Containerfile .

kubectl apply -f .\charts\virtual-kubelet.yaml
kubectl apply -f .\charts\deploy.yaml


kubectl delete -f .\charts\virtual-kubelet.yaml
kubectl delete -f .\charts\deploy.yaml


# Quirks
## Resources 
https://cloud.google.com/run/docs/configuring/services/cpu