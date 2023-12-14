export APISERVER_CERT_LOCATION="/tmp/docker-desktop-root/mnt/host/c/Users/james/Documents/GitHub/virtual-kubelet/cmd/virtual-kubelet/apiserver.crt"
export APISERVER_KEY_LOCATION="/tmp/docker-desktop-root/mnt/host/c/Users/james/Documents/GitHub/virtual-kubelet/cmd/virtual-kubelet/apiserver.key"
export APISERVER_CA_CERT_LOCATION="/tmp/docker-desktop-root/mnt/host/c/Users/james/Documents/GitHub/virtual-kubelet/cmd/virtual-kubelet/ca.crt"
export KUBECONFIG="/tmp/docker-desktop-root/mnt/host/c/Users/james/.kube/config"

./virtual-kubelet --provider mock --kubeconfig /tmp/docker-desktop-root/mnt/host/c/Users/james/.kube/config

./virtual-kubelet --provider mock --kubeconfig C:\Users\james\.kube\config