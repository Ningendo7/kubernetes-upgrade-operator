/*
Copyright 2026.

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

package v1alpha1

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/discovery"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

var _ = Describe("KubernetesUpgrade Webhook", func() {
	// Test targetVersions are computed relative to the real envtest
	// apiserver's own reported version (via the same DiscoveryClient the
	// production webhook uses), rather than hardcoded, so these tests
	// don't silently break if the pinned envtest Kubernetes version changes.
	var (
		validator *KubernetesUpgradeCustomValidator
		major     int64
		minor     int64
	)

	BeforeEach(func() {
		discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		validator = &KubernetesUpgradeCustomValidator{DiscoveryClient: discoveryClient}

		serverVersion, err := discoveryClient.ServerVersion()
		Expect(err).NotTo(HaveOccurred())

		parsed, err := k8sutil.ParseVersion(serverVersion.GitVersion)
		Expect(err).NotTo(HaveOccurred())
		major = int64(parsed.Major())
		minor = int64(parsed.Minor())
	})

	Context("When creating or updating KubernetesUpgrade under Validating Webhook", func() {
		It("rejects a targetVersion that cannot be parsed", func() {
			obj := &upgradev1alpha1.KubernetesUpgrade{
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: "not-a-version"},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})

		It("rejects a downgrade by default", func() {
			obj := &upgradev1alpha1.KubernetesUpgrade{
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{
					TargetVersion: fmt.Sprintf("v%d.%d.0", major, minor-1),
				},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("lower than"))
		})

		It("allows a downgrade when spec.safety.allowDowngrade is set", func() {
			obj := &upgradev1alpha1.KubernetesUpgrade{
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{
					TargetVersion: fmt.Sprintf("v%d.%d.0", major, minor-1),
					Safety:        &upgradev1alpha1.SafetyPolicy{AllowDowngrade: true},
				},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("allows a reasonable single-minor upgrade", func() {
			obj := &upgradev1alpha1.KubernetesUpgrade{
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{
					TargetVersion: fmt.Sprintf("v%d.%d.0", major, minor+1),
				},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects an implausibly large hop count", func() {
			obj := &upgradev1alpha1.KubernetesUpgrade{
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{
					TargetVersion: fmt.Sprintf("v%d.%d.0", major, minor+maxHopCount+1),
				},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("almost certainly a mistake"))
		})

		It("applies the same checks on update", func() {
			oldObj := &upgradev1alpha1.KubernetesUpgrade{
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: fmt.Sprintf("v%d.%d.0", major, minor+1)},
			}
			newObj := &upgradev1alpha1.KubernetesUpgrade{
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: fmt.Sprintf("v%d.%d.0", major, minor-1)},
			}
			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
		})
	})
})
