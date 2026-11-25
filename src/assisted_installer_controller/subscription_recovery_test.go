package assisted_installer_controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-service/models"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("recoverWedgedSubscriptions", func() {
	const (
		namespace = "openshift-workload-availability"
		planName  = "install-7m44b"
		nhc       = "node-healthcheck-operator"
		far       = "fence-agents-remediation"
	)

	var (
		ctrl *gomock.Controller
		kc   *k8s_client.MockK8SClient
		c    *controller

		nhcKey  = types.NamespacedName{Namespace: namespace, Name: nhc}
		farKey  = types.NamespacedName{Namespace: namespace, Name: far}
		planKey = types.NamespacedName{Namespace: namespace, Name: planName}
	)

	csvOf := func(name string) string { return name + ".v1.0.0" }
	// labelOf is the operator label built from the package name, since the test
	// subscriptions carry no OLM operator label.
	labelOf := func(name string) string { return "operators.coreos.com/" + name + "." + namespace }

	installPlanFailed := olmv1alpha1.SubscriptionCondition{
		Type:    olmv1alpha1.SubscriptionInstallPlanFailed,
		Status:  corev1.ConditionTrue,
		Reason:  "InstallComponentFailed",
		Message: `failed calling webhook "compatibilityrequirement.operator.openshift.io"`,
	}
	installPlanMissing := olmv1alpha1.SubscriptionCondition{
		Type:   olmv1alpha1.SubscriptionInstallPlanMissing,
		Status: corev1.ConditionTrue,
		Reason: "ReferencedInstallPlanNotFound",
	}

	// subscription returns a subscription referencing planName, with the given conditions.
	subscription := func(name string, conds ...olmv1alpha1.SubscriptionCondition) olmv1alpha1.Subscription {
		return olmv1alpha1.Subscription{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       namespace,
				Labels:          map[string]string{"app": name},
				Annotations:     map[string]string{"note": "keep"},
				ResourceVersion: "12345",
				UID:             "old-uid",
				Generation:      3,
			},
			Spec: &olmv1alpha1.SubscriptionSpec{
				CatalogSource:          "redhat-operators",
				CatalogSourceNamespace: "openshift-marketplace",
				Package:                name,
				Channel:                "stable",
			},
			Status: olmv1alpha1.SubscriptionStatus{
				CurrentCSV:     csvOf(name),
				State:          olmv1alpha1.SubscriptionStateAtLatest,
				InstallPlanRef: &corev1.ObjectReference{Namespace: namespace, Name: planName},
				Conditions:     conds,
			},
		}
	}
	subs := func(s ...olmv1alpha1.Subscription) []olmv1alpha1.Subscription { return s }

	csvInPhase := func(phase olmv1alpha1.ClusterServiceVersionPhase) *olmv1alpha1.ClusterServiceVersion {
		return &olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: phase}}
	}
	listedCSV := func(name string, labels map[string]string) olmv1alpha1.ClusterServiceVersion {
		return olmv1alpha1.ClusterServiceVersion{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}}
	}
	notFound := func(resource, name string) error {
		return apierrors.NewNotFound(schema.GroupResource{Group: "operators.coreos.com", Resource: resource}, name)
	}
	alreadyExists := apierrors.NewAlreadyExists(schema.GroupResource{Group: "operators.coreos.com", Resource: "subscriptions"}, nhc)

	// expectCSVCleanup expects the stranded-CSV cleanup of one subscription whose only
	// CSV is Pending.
	expectCSVCleanup := func(name string) *gomock.Call {
		kc.EXPECT().ListCSVs(namespace, labelOf(name)).Return(nil, nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(name)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		return kc.EXPECT().DeleteCSV(namespace, csvOf(name)).Return(nil).Times(1)
	}

	// expectRecreate expects one full recovery of a single subscription on planName.
	expectRecreate := func(name string) {
		key := types.NamespacedName{Namespace: namespace, Name: name}
		kc.EXPECT().DeleteSubscription(key).Return(nil).Times(1)
		expectCSVCleanup(name)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)
	}

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		kc = k8s_client.NewMockK8SClient(ctrl)
		c = newController(logrus.New(), ControllerConfig{}, nil, nil, kc, nil)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("does nothing when no subscription is wedged", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc), subscription(far)), nil).Times(1)

		Expect(c.recoverWedgedSubscriptions()).To(BeFalse())
		Expect(c.subscriptionRecovery).To(BeEmpty())
	})

	It("ignores wedge conditions that are not True", func() {
		notFailed := installPlanFailed
		notFailed.Status = corev1.ConditionFalse
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, notFailed)), nil).Times(1)

		Expect(c.recoverWedgedSubscriptions()).To(BeFalse())
	})

	It("reports recovery as outstanding when the subscriptions cannot be listed", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(nil, fmt.Errorf("connection refused")).Times(1)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())
	})

	It("recreates a wedged subscription, deleting its stranded CSV and the failed plan first", func() {
		sub := subscription(nhc, installPlanFailed)
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		deleteSub := kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		deleteCSV := expectCSVCleanup(nhc).After(deleteSub)
		deletePlan := kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1).After(deleteCSV)
		kc.EXPECT().CreateSubscription(gomock.Any()).DoAndReturn(func(created *olmv1alpha1.Subscription) error {
			Expect(created.Name).To(Equal(nhc))
			Expect(created.Namespace).To(Equal(namespace))
			Expect(created.Labels).To(Equal(sub.Labels))
			Expect(created.Annotations).To(Equal(sub.Annotations))
			Expect(created.Spec).To(Equal(sub.Spec))
			// Server-populated fields would make the create fail.
			Expect(created.ResourceVersion).To(BeEmpty())
			Expect(created.UID).To(BeEmpty())
			Expect(created.Generation).To(BeZero())
			Expect(created.Status).To(Equal(olmv1alpha1.SubscriptionStatus{}))
			return nil
		}).Times(1).After(deletePlan)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())

		state := c.subscriptionRecovery[nhcKey]
		Expect(state.attempts).To(Equal(1))
		Expect(state.pending).To(BeNil())
	})

	It("recovers every subscription that shares a failed plan together, including ones it does not monitor", func() {
		// The node-healthcheck case: OLM put fence-agents-remediation and
		// node-healthcheck in one InstallPlan. Recreating node-healthcheck alone let
		// OLM reuse the failed plan, so it failed again at once.
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).
			Return(subs(subscription(far, installPlanFailed), subscription(nhc, installPlanFailed)), nil).Times(1)
		deleteFar := kc.EXPECT().DeleteSubscription(farKey).Return(nil).Times(1)
		deleteNhc := kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		expectCSVCleanup(far)
		expectCSVCleanup(nhc)
		deletePlan := kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1).After(deleteFar).After(deleteNhc)
		var created []string
		kc.EXPECT().CreateSubscription(gomock.Any()).DoAndReturn(func(s *olmv1alpha1.Subscription) error {
			created = append(created, s.Name)
			return nil
		}).Times(2).After(deletePlan)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())

		Expect(created).To(ConsistOf(far, nhc))
		Expect(c.subscriptionRecovery[farKey].attempts).To(Equal(1))
		Expect(c.subscriptionRecovery[nhcKey].attempts).To(Equal(1))
	})

	It("waits while another subscription on the same plan is not wedged yet", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).
			Return(subs(subscription(far, installPlanFailed), subscription(nhc)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(gomock.Any()).Times(0)
		kc.EXPECT().DeleteInstallPlan(gomock.Any()).Times(0)

		c.recoverWedgedSubscriptions()

		Expect(c.subscriptionRecovery).To(BeEmpty())
	})

	It("recovers subscriptions on different plans independently", func() {
		other := subscription(far, installPlanFailed)
		other.Namespace = "openshift-workload-maintenance"
		other.Status.InstallPlanRef = &corev1.ObjectReference{Namespace: other.Namespace, Name: "install-jprqh"}
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed), other), nil).Times(1)

		expectRecreate(nhc)
		otherKey := types.NamespacedName{Namespace: other.Namespace, Name: far}
		kc.EXPECT().DeleteSubscription(otherKey).Return(nil).Times(1)
		kc.EXPECT().ListCSVs(other.Namespace, "operators.coreos.com/"+far+"."+other.Namespace).Return(nil, nil).Times(1)
		kc.EXPECT().GetCSV(other.Namespace, csvOf(far)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(other.Namespace, csvOf(far)).Return(nil).Times(1)
		kc.EXPECT().DeleteInstallPlan(types.NamespacedName{Namespace: other.Namespace, Name: "install-jprqh"}).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())
	})

	It("recreates a subscription whose plan is already gone", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanMissing)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		expectCSVCleanup(nhc)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(notFound("installplans", planName)).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()

		Expect(c.subscriptionRecovery[nhcKey].attempts).To(Equal(1))
	})

	It("recreates a wedged subscription that references no plan, without deleting a plan", func() {
		sub := subscription(nhc, installPlanFailed)
		sub.Status.InstallPlanRef = nil
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		expectCSVCleanup(nhc)
		kc.EXPECT().DeleteInstallPlan(gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()
	})

	It("skips subscriptions that are being deleted", func() {
		sub := subscription(nhc, installPlanFailed)
		sub.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		kc.EXPECT().DeleteSubscription(gomock.Any()).Times(0)

		Expect(c.recoverWedgedSubscriptions()).To(BeFalse())
	})

	It("never deletes a CSV that succeeded", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		kc.EXPECT().ListCSVs(namespace, labelOf(nhc)).Return(nil, nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhaseSucceeded), nil).Times(1)
		kc.EXPECT().DeleteCSV(gomock.Any(), gomock.Any()).Times(0)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()
	})

	It("still recreates the subscription when its CSV is already gone", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		kc.EXPECT().ListCSVs(namespace, labelOf(nhc)).Return(nil, nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(nil, notFound("clusterserviceversions", csvOf(nhc))).Times(1)
		kc.EXPECT().DeleteCSV(gomock.Any(), gomock.Any()).Times(0)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()

		Expect(c.subscriptionRecovery[nhcKey].pending).To(BeNil())
	})

	It("deletes an older CSV of the operator that the subscription no longer references", func() {
		// The mtv case: OLM moved currentCSV from v2.12.10 to v5.0.0 while v2.12.10 was
		// still Pending. Leaving v2.12.10 behind makes the recreated subscription fail
		// with ResolutionFailed "... exists and is not referenced by a subscription".
		const orphan = "node-healthcheck-operator.v0.9.0"
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		kc.EXPECT().ListCSVs(namespace, labelOf(nhc)).Return([]olmv1alpha1.ClusterServiceVersion{
			listedCSV(orphan, map[string]string{labelOf(nhc): ""}),
			listedCSV(csvOf(nhc), map[string]string{labelOf(nhc): ""}),
		}, nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, csvOf(nhc)).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, orphan).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		deleteOrphan := kc.EXPECT().DeleteCSV(namespace, orphan).Return(nil).Times(1)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1).After(deleteOrphan)

		c.recoverWedgedSubscriptions()
	})

	It("never deletes a CSV copied in from an operator installed for all namespaces", func() {
		const copied = "cluster-observability-operator.v1.5.5"
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		kc.EXPECT().ListCSVs(namespace, labelOf(nhc)).Return([]olmv1alpha1.ClusterServiceVersion{
			listedCSV(copied, map[string]string{labelOf(nhc): "", copiedCSVLabel: "openshift-cluster-observability-operator"}),
		}, nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, csvOf(nhc)).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, copied).Times(0)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()
	})

	It("looks up the operator's CSVs by the OLM label on the subscription when it has one", func() {
		const subLabel = "operators.coreos.com/node-healthcheck-operator.custom"
		sub := subscription(nhc, installPlanFailed)
		sub.Labels = map[string]string{"app": nhc, subLabel: ""}
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		kc.EXPECT().ListCSVs(namespace, subLabel).Return(nil, nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, csvOf(nhc)).Return(nil).Times(1)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()
	})

	It("still cleans up the referenced CSV and recreates when the CSVs cannot be listed", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		kc.EXPECT().ListCSVs(namespace, labelOf(nhc)).Return(nil, fmt.Errorf("timeout")).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, csvOf(nhc)).Return(nil).Times(1)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()

		Expect(c.subscriptionRecovery[nhcKey].pending).To(BeNil())
	})

	It("waits out the backoff before recreating the same subscription again", func() {
		sub := subscription(nhc, installPlanFailed)
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		expectRecreate(nhc)
		c.recoverWedgedSubscriptions()

		// Still wedged straight away: only checked, not recreated.
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())
		Expect(c.subscriptionRecovery[nhcKey].attempts).To(Equal(1))

		// Once the backoff has passed it is recreated again.
		c.subscriptionRecovery[nhcKey].lastAttempt = time.Now().Add(-subscriptionRecoveryBackoff)
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		expectRecreate(nhc)
		c.recoverWedgedSubscriptions()
		Expect(c.subscriptionRecovery[nhcKey].attempts).To(Equal(2))
	})

	It("gives up after maxSubscriptionRecoveries recreations and stops counting it as outstanding", func() {
		c.subscriptionRecovery[nhcKey] = &subscriptionRecoveryState{attempts: maxSubscriptionRecoveries}
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(2)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteSubscription(gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Times(0)

		c.recoverWedgedSubscriptions()
		Expect(c.subscriptionRecovery[nhcKey].gaveUp).To(BeTrue())
		Expect(c.recoverWedgedSubscriptions()).To(BeFalse())
	})

	It("keeps a recreated subscription outstanding until its CSV succeeds", func() {
		c.subscriptionRecovery[nhcKey] = &subscriptionRecoveryState{attempts: 1, lastAttempt: time.Now()}
		installing := subscription(nhc)

		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(installing), nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhaseInstalling), nil).Times(1)
		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())

		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(installing), nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhaseSucceeded), nil).Times(1)
		Expect(c.recoverWedgedSubscriptions()).To(BeFalse())
		Expect(c.subscriptionRecovery[nhcKey].installed).To(BeTrue())

		// Not checked again once installed.
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(installing), nil).Times(1)
		Expect(c.recoverWedgedSubscriptions()).To(BeFalse())
	})

	It("does not delete the plan, the CSV or create anything for a subscription that could not be deleted", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(fmt.Errorf("forbidden")).Times(1)
		kc.EXPECT().DeleteCSV(gomock.Any(), gomock.Any()).Times(0)
		kc.EXPECT().DeleteInstallPlan(gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Times(0)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())

		state := c.subscriptionRecovery[nhcKey]
		Expect(state.pending).To(BeNil())
		// The attempt still counts, so a persistent delete failure is also backed off.
		Expect(state.attempts).To(Equal(1))
	})

	It("keeps the plan while any subscription in the group could not be deleted, and recreates the others", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).
			Return(subs(subscription(far, installPlanFailed), subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(farKey).Return(fmt.Errorf("forbidden")).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		expectCSVCleanup(nhc)
		kc.EXPECT().DeleteInstallPlan(gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())
	})

	It("still recreates the subscriptions when the plan cannot be deleted", func() {
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		expectCSVCleanup(nhc)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(fmt.Errorf("timeout")).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscriptions()

		Expect(c.subscriptionRecovery[nhcKey].pending).To(BeNil())
	})

	It("retries a failed create on the next poll before anything else", func() {
		sub := subscription(nhc, installPlanFailed)
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(sub), nil).Times(1)
		kc.EXPECT().DeleteSubscription(nhcKey).Return(nil).Times(1)
		expectCSVCleanup(nhc)
		kc.EXPECT().DeleteInstallPlan(planKey).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(fmt.Errorf("connection refused")).Times(1)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())
		Expect(c.subscriptionRecovery[nhcKey].pending).NotTo(BeNil())

		// Next poll: the create is retried; the subscription is not in the list yet.
		create := kc.EXPECT().CreateSubscription(gomock.Any()).DoAndReturn(func(created *olmv1alpha1.Subscription) error {
			Expect(created.Spec).To(Equal(sub.Spec))
			return nil
		}).Times(1)
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(), nil).Times(1).After(create)

		c.recoverWedgedSubscriptions()
		Expect(c.subscriptionRecovery[nhcKey].pending).To(BeNil())
		Expect(c.subscriptionRecovery[nhcKey].attempts).To(Equal(1))
	})

	It("keeps retrying the create while the old subscription is terminating", func() {
		c.subscriptionRecovery[nhcKey] = &subscriptionRecoveryState{
			attempts: 1,
			pending:  subscriptionForRecreate(ptr(subscription(nhc))),
		}
		terminating := subscription(nhc, installPlanFailed)
		terminating.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(alreadyExists).Times(1)
		kc.EXPECT().GetSubscription(nhcKey).Return(&terminating, nil).Times(1)
		// The terminating original is listed but skipped.
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(terminating), nil).Times(1)
		kc.EXPECT().DeleteSubscription(gomock.Any()).Times(0)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())
		Expect(c.subscriptionRecovery[nhcKey].pending).NotTo(BeNil())
	})

	It("keeps retrying the create when the existing subscription cannot be read", func() {
		c.subscriptionRecovery[nhcKey] = &subscriptionRecoveryState{
			attempts: 1,
			pending:  subscriptionForRecreate(ptr(subscription(nhc))),
		}
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(alreadyExists).Times(1)
		kc.EXPECT().GetSubscription(nhcKey).Return(nil, fmt.Errorf("connection refused")).Times(1)
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(), nil).Times(1)

		Expect(c.recoverWedgedSubscriptions()).To(BeTrue())
		Expect(c.subscriptionRecovery[nhcKey].pending).NotTo(BeNil())
	})

	It("treats an existing live subscription as recreated, so a second wedge is still detected", func() {
		// An earlier create succeeded on the server but its response was lost.
		c.subscriptionRecovery[nhcKey] = &subscriptionRecoveryState{
			attempts:    1,
			lastAttempt: time.Now().Add(-2 * subscriptionRecoveryBackoff),
			pending:     subscriptionForRecreate(ptr(subscription(nhc))),
		}
		live := subscription(nhc)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(alreadyExists).Times(1)
		kc.EXPECT().GetSubscription(nhcKey).Return(&live, nil).Times(1)
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(live), nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhaseInstalling), nil).Times(1)

		c.recoverWedgedSubscriptions()

		state := c.subscriptionRecovery[nhcKey]
		Expect(state.pending).To(BeNil())
		Expect(time.Since(state.lastAttempt)).To(BeNumerically("<", time.Minute))
	})

	It("restarts the backoff when a delayed create finally succeeds", func() {
		// The delete happened long ago, but the create only succeeds now: the new
		// subscription must still get the full backoff before it can be recreated.
		c.subscriptionRecovery[nhcKey] = &subscriptionRecoveryState{
			attempts:    1,
			lastAttempt: time.Now().Add(-2 * subscriptionRecoveryBackoff),
			pending:     subscriptionForRecreate(ptr(subscription(nhc))),
		}
		create := kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)
		// Reported wedged straight away (a stale or quick failure): only checked.
		kc.EXPECT().ListSubscriptions(metav1.NamespaceAll).Return(subs(subscription(nhc, installPlanFailed)), nil).Times(1).After(create)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteSubscription(gomock.Any()).Times(0)

		c.recoverWedgedSubscriptions()

		Expect(c.subscriptionRecovery[nhcKey].attempts).To(Equal(1))
		Expect(time.Since(c.subscriptionRecovery[nhcKey].lastAttempt)).To(BeNumerically("<", time.Minute))
	})

	It("is not part of the readiness check any more", func() {
		operator := models.MonitoredOperator{Name: "nhc", Namespace: namespace, SubscriptionName: nhc}
		kc.EXPECT().GetCSVFromSubscription(namespace, nhc).Return(csvOf(nhc), nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvOf(nhc)).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().ListSubscriptions(gomock.Any()).Times(0)
		kc.EXPECT().GetSubscription(gomock.Any()).Times(0)

		ready, _, err := c.getReadyOperators([]models.MonitoredOperator{operator}, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeEmpty())
	})
})

func ptr(s olmv1alpha1.Subscription) *olmv1alpha1.Subscription { return &s }
