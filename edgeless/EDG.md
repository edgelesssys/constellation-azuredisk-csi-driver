# Constellation modifications & documentation

## Prerequisites

Create a docker-registry secret to configure pull access for the driver:

```shell
kubectl create secret docker-registry regcred \
    --docker-server=DOCKER_REGISTRY_SERVER \
    --docker-username=DOCKER_USER \
    --docker-password=DOCKER_PASSWORD \
    --docker-email=DOCKER_EMAIL
    --namespace=kube-system
```

## Deploying the driver

Use `helm` to deploy the driver to your cluster:

```shell
helm install azuredisk-csi-driver charts/edgeless/v1.3.0/azuredisk-csi-driver-v1.3.0.tgz \
    --namespace kube-system \
    --set linux.distro=fedora \
    --set controller.replicas=1
```

See [helm configuration](../charts/README.md#V1-Parameters) for a detailed list on configuration options.

Alternatively, you can also use `kubectl` to deploy the driver to the cluster:

```shell
kubectl apply -f deploy/edgeless/v1.0.0
```

## Use

Create a new storage class using the driver:

```shell
cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: encrypted-storage
provisioner: azuredisk.csi.confidential.cloud
parameters:
  skuName: StandardSSD_LRS
reclaimPolicy: Delete
volumeBindingMode: Immediate
allowVolumeExpansion: true
EOF
```

Deploy a Pod with persistent volume claims:

```shell
cat <<EOF | kubectl apply -f -
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: pvc-example
  namespace: default
spec:
  accessModes:
  - ReadWriteOnce
  storageClassName: encrypted-storage
  resources:
    requests:
      storage: 10Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: web-server
  namespace: default
spec:
  containers:
  - name: web-server
    image: nginx
    volumeMounts:
    - mountPath: /var/lib/www/html
      name: mypvc
  volumes:
  - name: mypvc
    persistentVolumeClaim:
      claimName: pvc-example
      readOnly: false
EOF
```

## Enabling integrity protection

By default the CSI driver will transparently encrypt all disks staged on the node.
Optionally, you can configure the driver to also apply integrity protection.

Please note that enabling integrity protection requires wiping the disk before use.
For small disks (10GB-20GB) this may only take a minute or two, while larger disks can take up to an hour or more, potentially blocking your Pods from starting for that time.
If you intend to provision large amounts of storage and Pod creation speed is important, we recommend to not use this option.

To enable integrity protection, create a storage class with an explicit file system type request and the integrity suffix.
The following is a storage class for integrity protected `ext4` formatted disks:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: integrity-protected
provisioner: azuredisk.csi.confidential.cloud
parameters:
  skuName: StandardSSD_LRS
  csi.storage.k8s.io/fstype: ext4-integrity
reclaimPolicy: Delete
volumeBindingMode: Immediate
allowVolumeExpansion: true
```

Please note that [volume expansion](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/) is not supported for integrity-protected disks.

## Cleanup

Remove the driver using helm:

```shell
helm uninstall azuredisk-csi-driver -n kube-system
```

Remove the driver using kubectl:

```shell
kubectl delete -f deploy/edgeless/
```

## Build your own driver

```shell
make REGISTRY=ghcr.io/edgelesssys IMAGE_NAME=encrypted-azure-csi-driver IMAGE_VERSION=test container
docker push ghcr.io/edgelesssys/encrypted-azure-csi-driver:test
```
