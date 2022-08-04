# CSI driver e2e tests

Run end to end tests using the kubernetes `e2e.test` binary.

Download the binary for your Kubernetes version:

```shell
K8S_VER=1.23.0
curl --location https://dl.k8s.io/v${K8S_VER}/kubernetes-test-linux-amd64.tar.gz | \
  tar --strip-components=3 -zxf - kubernetes/test/bin/e2e.test
```

For an overview on how to run tests read the [Kubernetes blog post](https://kubernetes.io/blog/2020/01/08/testing-of-csi-drivers/#end-to-end-testing).

## Running the test suite

1. Set up the CSI driver

    ```shell
    helm install azuredisk-csi-driver charts/edgeless/v1.0.0/azuredisk-csi-driver-v1.0.0.tgz \
        --namespace kube-system \
        --set controller.runOnMaster=true \
        --set linux.distro=fedora \
        --set controller.replicas=1
    ```

1. Run the tests

    ```shell
    ./e2e.test \
        -ginkgo.v \
        -ginkgo.focus='External.Storage' \
        -ginkgo.skip='\[Disruptive\]' \
        -storage.testdriver=driver.yaml
    ```
