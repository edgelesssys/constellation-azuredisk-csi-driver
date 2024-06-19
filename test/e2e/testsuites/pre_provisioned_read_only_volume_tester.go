/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//nolint:dupl
package testsuites

import (
	"context"
	"fmt"

	"sigs.k8s.io/azuredisk-csi-driver/test/e2e/driver"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
)

// PreProvisionedReadOnlyVolumeTest will provision required PV(s), PVC(s) and Pod(s)
// Testing that the Pod(s) cannot write to the volume when mounted
type PreProvisionedReadOnlyVolumeTest struct {
	CSIDriver     driver.PreProvisionedVolumeTestDriver
	Pods          []PodDetails
	VolumeContext map[string]string
}

func (t *PreProvisionedReadOnlyVolumeTest) Run(ctx context.Context, client clientset.Interface, namespace *v1.Namespace) {
	for _, pod := range t.Pods {
		expectedReadOnlyLog := "Read-only file system"
		if pod.IsWindows {
			expectedReadOnlyLog = "FileOpenFailure"
		}

		tpod, cleanup := pod.SetupWithPreProvisionedVolumes(ctx, client, namespace, t.CSIDriver, t.VolumeContext)
		// defer must be called here for resources not get removed before using them
		for i := range cleanup {
			defer cleanup[i](ctx)
		}

		ginkgo.By("deploying the pod")
		tpod.Create(ctx)
		defer tpod.Cleanup(ctx)
		ginkgo.By("checking that the pod's command exits with an error")
		tpod.WaitForFailure(ctx)
		ginkgo.By("checking that pod logs contain expected message")
		body, err := tpod.Logs(ctx)
		framework.ExpectNoError(err, fmt.Sprintf("Error getting logs for pod %s: %v", tpod.pod.Name, err))
		gomega.Expect(string(body)).To(gomega.ContainSubstring(expectedReadOnlyLog))
	}
}
