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

To enable integrity protection support for the CSI driver, set `--integrity` to `true` in `deploy/edgeless/csi-azuredisk-node.yaml` and apply the changes:
```shell
sed -i s/--integrity=false/--integrity=true/g ./deploy/edgeless/csi-azuredisk-node.yaml
kubectl apply -f deploy/edgeless
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
