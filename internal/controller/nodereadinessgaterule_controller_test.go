/*
Copyright 2025.

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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nodereadinessiov1alpha1 "github.com/ajaysundark/node-readiness-gate-controller/api/v1alpha1"
)

var _ = Describe("NodeReadinessGateRule Controller", func() {
	var (
		ctx                 context.Context
		readinessController *ReadinessGateController
		ruleReconciler      *RuleReconciler
		nodeReconciler      *NodeReconciler
		scheme              *runtime.Scheme
		fakeClientset       *fake.Clientset
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(nodereadinessiov1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		fakeClientset = fake.NewSimpleClientset()
		readinessController = &ReadinessGateController{
			Client:    k8sClient,
			Scheme:    scheme,
			clientset: fakeClientset,
			ruleCache: make(map[string]*nodereadinessiov1alpha1.NodeReadinessGateRule),
		}

		ruleReconciler = &RuleReconciler{
			Client:     k8sClient,
			Scheme:     scheme,
			Controller: readinessController,
		}

		nodeReconciler = &NodeReconciler{
			Client:     k8sClient,
			Scheme:     scheme,
			Controller: readinessController,
		}
	})

	Context("Rule Reconciliation", func() {
		It("should handle rule creation and update cache", func() {
			rule := &nodereadinessiov1alpha1.NodeReadinessGateRule{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rule",
				},
				Spec: nodereadinessiov1alpha1.NodeReadinessGateRuleSpec{
					Conditions: []nodereadinessiov1alpha1.ConditionRequirement{
						{Type: "Ready", RequiredStatus: corev1.ConditionTrue},
					},
					Taint: nodereadinessiov1alpha1.TaintSpec{
						Key:    "test-taint",
						Effect: corev1.TaintEffectNoSchedule,
					},
					EnforcementMode: nodereadinessiov1alpha1.EnforcementModeBootstrapOnly,
				},
			}

			Expect(k8sClient.Create(ctx, rule)).To(Succeed())

			Eventually(func() error {
				_, err := ruleReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "test-rule"},
				})
				return err
			}).Should(Succeed())

			// Verify rule is in cache
			readinessController.ruleCacheMutex.RLock()
			cachedRule, exists := readinessController.ruleCache["test-rule"]
			readinessController.ruleCacheMutex.RUnlock()
			Expect(exists).To(BeTrue())
			Expect(cachedRule.Spec.Taint.Key).To(Equal("test-taint"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, rule)).To(Succeed())
		})

		It("should handle rule deletion and remove from cache", func() {
			rule := &nodereadinessiov1alpha1.NodeReadinessGateRule{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rule-delete",
				},
				Spec: nodereadinessiov1alpha1.NodeReadinessGateRuleSpec{
					Conditions: []nodereadinessiov1alpha1.ConditionRequirement{
						{Type: "Ready", RequiredStatus: corev1.ConditionTrue},
					},
					Taint: nodereadinessiov1alpha1.TaintSpec{
						Key:    "test-taint",
						Effect: corev1.TaintEffectNoSchedule,
					},
					EnforcementMode: nodereadinessiov1alpha1.EnforcementModeBootstrapOnly,
				},
			}

			Expect(k8sClient.Create(ctx, rule)).To(Succeed())

			// First reconcile to add to cache
			_, err := ruleReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rule-delete"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Delete the rule
			Expect(k8sClient.Delete(ctx, rule)).To(Succeed())

			// Second reconcile should remove from cache
			Eventually(func() bool {
				_, err := ruleReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "test-rule-delete"},
				})
				Expect(err).NotTo(HaveOccurred())

				readinessController.ruleCacheMutex.RLock()
				_, exists := readinessController.ruleCache["test-rule-delete"]
				readinessController.ruleCacheMutex.RUnlock()
				return !exists
			}).Should(BeTrue())
		})

		It("should handle dry run mode", func() {
			rule := &nodereadinessiov1alpha1.NodeReadinessGateRule{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dry-run-rule",
				},
				Spec: nodereadinessiov1alpha1.NodeReadinessGateRuleSpec{
					Conditions: []nodereadinessiov1alpha1.ConditionRequirement{
						{Type: "Ready", RequiredStatus: corev1.ConditionTrue},
					},
					Taint: nodereadinessiov1alpha1.TaintSpec{
						Key:    "dry-run-taint",
						Effect: corev1.TaintEffectNoSchedule,
					},
					EnforcementMode: nodereadinessiov1alpha1.EnforcementModeBootstrapOnly,
					DryRun:          true,
				},
			}

			Expect(k8sClient.Create(ctx, rule)).To(Succeed())

			_, err := ruleReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "dry-run-rule"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify dry run results are populated
			Eventually(func() *nodereadinessiov1alpha1.DryRunResults {
				updatedRule := &nodereadinessiov1alpha1.NodeReadinessGateRule{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "dry-run-rule"}, updatedRule)
				if err != nil {
					return nil
				}
				return updatedRule.Status.DryRunResults
			}).ShouldNot(BeNil())

			// Cleanup
			Expect(k8sClient.Delete(ctx, rule)).To(Succeed())
		})
	})

	Context("Node Processing", func() {
		var testNode *corev1.Node

		BeforeEach(func() {
			testNode = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Labels: map[string]string{
						"node-role.kubernetes.io/worker": "",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   "Ready",
							Status: corev1.ConditionTrue,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, testNode)).To(Succeed())
		})

		AfterEach(func() {
			if testNode != nil {
				k8sClient.Delete(ctx, testNode)
			}
		})

		It("should process node changes", func() {
			rule := &nodereadinessiov1alpha1.NodeReadinessGateRule{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-test-rule",
				},
				Spec: nodereadinessiov1alpha1.NodeReadinessGateRuleSpec{
					Conditions: []nodereadinessiov1alpha1.ConditionRequirement{
						{Type: "Ready", RequiredStatus: corev1.ConditionTrue},
					},
					Taint: nodereadinessiov1alpha1.TaintSpec{
						Key:    "node-test-taint",
						Effect: corev1.TaintEffectNoSchedule,
					},
					EnforcementMode: nodereadinessiov1alpha1.EnforcementModeBootstrapOnly,
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, rule)).To(Succeed())
			defer k8sClient.Delete(ctx, rule)

			// First add rule to cache
			readinessController.updateRuleCache(rule)

			// Process node
			_, err := nodeReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-node"},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Core Logic Tests", func() {
		It("should evaluate conditions correctly", func() {
			node := &corev1.Node{
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: "Ready", Status: corev1.ConditionTrue},
						{Type: "NetworkReady", Status: corev1.ConditionFalse},
					},
				},
			}

			// Test condition exists and matches
			status := readinessController.getConditionStatus(node, "Ready")
			Expect(status).To(Equal(corev1.ConditionTrue))

			// Test condition exists but doesn't match
			status = readinessController.getConditionStatus(node, "NetworkReady")
			Expect(status).To(Equal(corev1.ConditionFalse))

			// Test missing condition
			status = readinessController.getConditionStatus(node, "StorageReady")
			Expect(status).To(Equal(corev1.ConditionUnknown))
		})

		It("should detect taints correctly", func() {
			node := &corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{Key: "test-key", Effect: corev1.TaintEffectNoSchedule, Value: "test-value"},
						{Key: "another-key", Effect: corev1.TaintEffectNoExecute},
					},
				},
			}

			taintSpec := nodereadinessiov1alpha1.TaintSpec{
				Key:    "test-key",
				Effect: corev1.TaintEffectNoSchedule,
			}

			hasTaint := readinessController.hasTaintBySpec(node, taintSpec)
			Expect(hasTaint).To(BeTrue())

			// Test non-existent taint
			nonExistentTaint := nodereadinessiov1alpha1.TaintSpec{
				Key:    "missing-key",
				Effect: corev1.TaintEffectNoSchedule,
			}

			hasTaint = readinessController.hasTaintBySpec(node, nonExistentTaint)
			Expect(hasTaint).To(BeFalse())
		})

		It("should check rule applicability correctly", func() {
			rule := &nodereadinessiov1alpha1.NodeReadinessGateRule{
				Spec: nodereadinessiov1alpha1.NodeReadinessGateRuleSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
			}

			// Node that matches
			matchingNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"node-role.kubernetes.io/worker": "",
					},
				},
			}

			applies := readinessController.ruleAppliesTo(rule, matchingNode)
			Expect(applies).To(BeTrue())

			// Node that doesn't match
			nonMatchingNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"node-role.kubernetes.io/control-plane": "",
					},
				},
			}

			applies = readinessController.ruleAppliesTo(rule, nonMatchingNode)
			Expect(applies).To(BeFalse())

			// Rule without selector should apply to all nodes
			ruleWithoutSelector := &nodereadinessiov1alpha1.NodeReadinessGateRule{
				Spec: nodereadinessiov1alpha1.NodeReadinessGateRuleSpec{},
			}

			applies = readinessController.ruleAppliesTo(ruleWithoutSelector, nonMatchingNode)
			Expect(applies).To(BeTrue())
		})

		It("should handle bootstrap completion tracking", func() {
			nodeName := "bootstrap-test-node"
			ruleName := "bootstrap-test-rule"

			// Initially not completed
			completed := readinessController.isBootstrapCompleted(nodeName, ruleName)
			Expect(completed).To(BeFalse())

			// Create a node for testing
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())
			defer k8sClient.Delete(ctx, node)

			// Mark as completed
			readinessController.markBootstrapCompleted(ctx, nodeName, ruleName)

			// Should now be completed
			Eventually(func() bool {
				return readinessController.isBootstrapCompleted(nodeName, ruleName)
			}).Should(BeTrue())
		})
	})
})
