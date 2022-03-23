# Constellation modifications & documentation

## Prerequisites

*Note*: The following steps are only required as long as we don't have a working cloud controller manager setup on Azure.

Constellation on Azure creates the required permissions by default.

1. Create a cloud provider config file

    A full list of options for the config file can be found [here.](https://kubernetes-sigs.github.io/cloud-provider-azure/install/configs/)
    See the following for a minimal example:

    ```javascript
    {
      "cloud":"AzurePublicCloud",
      "tenantId":"<tenant_ID>",
      "subscriptionId":"<subscription_ID>",
      "useManagedIdentityExtension":true,
      "resourceGroup":"<constellation_resource_group>",
      "location":"North Europe",
      "vmType":"vmss",
      "useInstanceMetadata":true
    }
    ```

    Save the file to `csi-credentials/azure.json`

1. Create a Kubernetes secret with the config

    ```shell
    cat <<EOF | kubectl apply -f -
    apiVersion: v1
    data:
      cloud-config: $(< csi-credentials/azure.json base64 -w0)
    kind: Secret
    metadata:
      name: azure-cloud-provider
      namespace: kube-system
    type: Opaque
    EOF
    ```

## Deploying the driver

[Only needed when pulling from a private repository] Create a pull secret:
```shell
kubectl create secret docker-registry regcred \
    --docker-server=DOCKER_REGISTRY_SERVER \
    --docker-username=DOCKER_USER \
    --docker-password=DOCKER_PASSWORD \
    --docker-email=DOCKER_EMAIL
    --namespace=constellation-csi-gcp
```

Use `kubectl` to deploy the driver to the cluster:
```shell
kubectl apply -f deploy/edgeless/
```

## Use

Create a new storage class using the driver:
```shell
cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: encrypted-storage
provisioner: disk.csi.azure.com
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
      storage: 20Gi
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

## Cleanup

Remove the driver by running the following:
```shell
kubectl delete -f deploy/edgeless/
```

## Build your own driver

```shell
make REGISTRY=ghcr.io/edgelesssys IMAGE_NAME=encrypted-azure-csi-driver IMAGE_VERSION=test container
push ghcr.io/edgelesssys/encrypted-azure-csi-driver:test
```
