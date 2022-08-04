# Azure Disk CSI driver for Constellation Kubernetes

- [Upstream source](https://github.com/kubernetes-sigs/azuredisk-csi-driver)
- [Constellation repo](https://github.com/edgelesssys/constellation)

## About

This driver allows a Constellation cluster to use [Azure Disk](https://azure.microsoft.com/en-us/services/storage/disks/) volume, csi plugin name: `azuredisk.csi.confidential.cloud`

### Driver parameters

Please refer to [`azuredisk.csi.confidential.cloud` driver parameters](./docs/driver-parameters.md)

### Install driver on a Constellation Kubernetes cluster

Use `helm` to deploy the driver to your cluster:

```shell
chart_version="v1.3.0"
helm install azuredisk-csi-driver charts/edgeless/${chart_version}/azuredisk-csi-driver-${chart_version}.tgz \
    --namespace kube-system \
    --set linux.distro=fedora \
    --set controller.replicas=1
```

See [helm configuration](../charts/README.md#V1-Parameters) for a detailed list on configuration options.

Remove the driver using helm:

```shell
helm uninstall azuredisk-csi-driver -n kube-system
```

## Examples

- [Basic usage](./deploy/example/e2e_usage.md)

## Features

- [Topology (Availability Zone)](./deploy/example/topology)
  - [ZRS disk support](./deploy/example/topology#zrs-disk-support)
- [Snapshot](./deploy/example/snapshot)
- [Volume Cloning](./deploy/example/cloning)
- [Volume Expansion](./deploy/example/resize)
- [Raw Block Volume](./deploy/example/rawblock)
- [Volume Limits](./deploy/example/volumelimits)
- [fsGroupPolicy](./deploy/example/fsgroup)
- [Tune disk performance on Linux](./docs/perf-profiles.md)
- Transparent disk encryption at node level
- Disk integrity protection

### Enabling integrity protection

By default the CSI driver will transparently encrypt all disks staged on the node.
Optionally, you can configure the driver to also apply integrity protection.

Please note that enabling integrity protection requires wiping the disk before use.
Disk wipe speeds are largely dependent on IOPS and performance tier of the disk.
If you intend to provision large amounts of storage and Pod creation speed is important,
we recommend to request high performance disks or to not use this option.

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
```

Please note that [volume expansion](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/) is not supported for integrity-protected disks.

## Troubleshooting

- [CSI driver troubleshooting guide](./docs/csi-debug.md)

## Limitations

- Please refer to [Azure Disk CSI Driver Limitations](./docs/limitations.md)

## Kubernetes Development

- Please refer to [development guide](./docs/csi-dev.md)

To build the driver container image:

```shell
driver_version=v0.0.0-test
make REGISTRY=ghcr.io/edgelesssys IMAGE_NAME=encrypted-azure-csi-driver IMAGE_VERSION=${driver_version} container
docker push ghcr.io/edgelesssys/encrypted-azure-csi-driver:${driver_version}
```

## Links

- [Kubernetes CSI Documentation](https://kubernetes-csi.github.io/docs/)
- [Container Storage Interface (CSI) Specification](https://github.com/container-storage-interface/spec)
