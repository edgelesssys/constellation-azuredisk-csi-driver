# fsGroup Support

[fsGroupPolicy](https://kubernetes-csi.github.io/docs/support-fsgroup.html) feature is Beta from Kubernetes 1.20, and disabled by default, follow below steps to enable this feature.

### Option#1: Enable fsGroupPolicy support in [driver helm installation](../../../charts)

add `--set feature.enableFSGroupPolicy=true` in helm installation command.

### Option#2: Enable fsGroupPolicy support on a cluster with CSI driver already installed

```console
kubectl delete CSIDriver azuredisk.csi.confidential.cloud
cat <<EOF | kubectl create -f -
apiVersion: storage.k8s.io/v1
kind: CSIDriver
metadata:
  name: azuredisk.csi.confidential.cloud
spec:
  attachRequired: true
  podInfoOnMount: false
  fsGroupPolicy: File
EOF
```
