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

var _ = Describe("recoverWedgedSubscription", func() {
	const (
		namespace = "openshift-operators-redhat"
		subName   = "loki-operator"
		csvName   = "loki-operator.v6.7.0"
	)

	var (
		ctrl     *gomock.Controller
		kc       *k8s_client.MockK8SClient
		c        *controller
		operator models.MonitoredOperator
		key      = types.NamespacedName{Namespace: namespace, Name: subName}
	)

	subscription := func(conds ...olmv1alpha1.SubscriptionCondition) *olmv1alpha1.Subscription {
		return &olmv1alpha1.Subscription{
			ObjectMeta: metav1.ObjectMeta{
				Name:            subName,
				Namespace:       namespace,
				Labels:          map[string]string{"app": "loki"},
				Annotations:     map[string]string{"note": "keep"},
				ResourceVersion: "12345",
				UID:             "old-uid",
				Generation:      3,
			},
			Spec: &olmv1alpha1.SubscriptionSpec{
				CatalogSource:          "redhat-operators",
				CatalogSourceNamespace: "openshift-marketplace",
				Package:                "loki-operator",
				Channel:                "stable-6.7",
			},
			Status: olmv1alpha1.SubscriptionStatus{
				CurrentCSV:   csvName,
				InstalledCSV: csvName,
				State:        olmv1alpha1.SubscriptionStateAtLatest,
				Conditions:   conds,
			},
		}
	}
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
	csvInPhase := func(phase olmv1alpha1.ClusterServiceVersionPhase) *olmv1alpha1.ClusterServiceVersion {
		return &olmv1alpha1.ClusterServiceVersion{Status: olmv1alpha1.ClusterServiceVersionStatus{Phase: phase}}
	}
	alreadyExists := apierrors.NewAlreadyExists(schema.GroupResource{Group: "operators.coreos.com", Resource: "subscriptions"}, subName)

	// expectRecreate expects one full recovery of a subscription whose CSV is Pending.
	expectRecreate := func(sub *olmv1alpha1.Subscription) {
		kc.EXPECT().GetSubscription(key).Return(sub, nil).Times(1)
		kc.EXPECT().DeleteSubscription(key).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvName).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, csvName).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)
	}

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		kc = k8s_client.NewMockK8SClient(ctrl)
		c = newController(logrus.New(), ControllerConfig{}, nil, nil, kc, nil)
		operator = models.MonitoredOperator{Name: "loki", Namespace: namespace, SubscriptionName: subName}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("does nothing to a subscription that is still progressing", func() {
		kc.EXPECT().GetSubscription(key).Return(subscription(), nil).Times(1)

		c.recoverWedgedSubscription(operator)

		Expect(c.subscriptionRecovery[key].attempts).To(Equal(0))
	})

	It("ignores wedge conditions that are not True", func() {
		notFailed := installPlanFailed
		notFailed.Status = corev1.ConditionFalse
		kc.EXPECT().GetSubscription(key).Return(subscription(notFailed), nil).Times(1)

		c.recoverWedgedSubscription(operator)

		Expect(c.subscriptionRecovery[key].attempts).To(Equal(0))
	})

	It("does nothing when the subscription cannot be read", func() {
		kc.EXPECT().GetSubscription(key).Return(nil, fmt.Errorf("connection refused")).Times(1)

		c.recoverWedgedSubscription(operator)

		Expect(c.subscriptionRecovery[key].attempts).To(Equal(0))
	})

	It("recreates a subscription whose install plan failed and deletes its stranded CSV", func() {
		sub := subscription(installPlanFailed)
		kc.EXPECT().GetSubscription(key).Return(sub, nil).Times(1)
		deleteSub := kc.EXPECT().DeleteSubscription(key).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvName).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		deleteCSV := kc.EXPECT().DeleteCSV(namespace, csvName).Return(nil).Times(1).After(deleteSub)
		kc.EXPECT().CreateSubscription(gomock.Any()).DoAndReturn(func(created *olmv1alpha1.Subscription) error {
			Expect(created.Name).To(Equal(subName))
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
		}).Times(1).After(deleteCSV)

		c.recoverWedgedSubscription(operator)

		state := c.subscriptionRecovery[key]
		Expect(state.attempts).To(Equal(1))
		Expect(state.pending).To(BeNil())
	})

	It("recreates a subscription whose install plan was deleted", func() {
		expectRecreate(subscription(installPlanMissing))

		c.recoverWedgedSubscription(operator)

		Expect(c.subscriptionRecovery[key].attempts).To(Equal(1))
	})

	It("deletes both CSVs when the subscription points at two", func() {
		sub := subscription(installPlanFailed)
		sub.Status.InstalledCSV = "loki-operator.v6.6.0"
		kc.EXPECT().GetSubscription(key).Return(sub, nil).Times(1)
		kc.EXPECT().DeleteSubscription(key).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvName).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, csvName).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, "loki-operator.v6.6.0").Return(csvInPhase(olmv1alpha1.CSVPhaseFailed), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, "loki-operator.v6.6.0").Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscription(operator)
	})

	It("never deletes a CSV that succeeded", func() {
		kc.EXPECT().GetSubscription(key).Return(subscription(installPlanFailed), nil).Times(1)
		kc.EXPECT().DeleteSubscription(key).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvName).Return(csvInPhase(olmv1alpha1.CSVPhaseSucceeded), nil).Times(1)
		kc.EXPECT().DeleteCSV(gomock.Any(), gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscription(operator)
	})

	It("still recreates the subscription when its CSV is already gone", func() {
		kc.EXPECT().GetSubscription(key).Return(subscription(installPlanFailed), nil).Times(1)
		kc.EXPECT().DeleteSubscription(key).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvName).
			Return(nil, apierrors.NewNotFound(schema.GroupResource{Resource: "clusterserviceversions"}, csvName)).Times(1)
		kc.EXPECT().DeleteCSV(gomock.Any(), gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(nil).Times(1)

		c.recoverWedgedSubscription(operator)

		Expect(c.subscriptionRecovery[key].pending).To(BeNil())
	})

	It("waits out the backoff before recreating the same subscription again", func() {
		sub := subscription(installPlanFailed)
		expectRecreate(sub)
		c.recoverWedgedSubscription(operator)

		// Still wedged straight away: only checked, not recreated.
		kc.EXPECT().GetSubscription(key).Return(sub, nil).Times(1)
		c.recoverWedgedSubscription(operator)
		Expect(c.subscriptionRecovery[key].attempts).To(Equal(1))

		// Once the backoff has passed it is recreated again.
		c.subscriptionRecovery[key].lastAttempt = time.Now().Add(-subscriptionRecoveryBackoff)
		expectRecreate(sub)
		c.recoverWedgedSubscription(operator)
		Expect(c.subscriptionRecovery[key].attempts).To(Equal(2))
	})

	It("gives up after maxSubscriptionRecoveries recreations", func() {
		c.subscriptionRecovery[key] = &subscriptionRecoveryState{attempts: maxSubscriptionRecoveries}
		kc.EXPECT().GetSubscription(key).Return(subscription(installPlanFailed), nil).Times(1)
		kc.EXPECT().DeleteSubscription(gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Times(0)

		c.recoverWedgedSubscription(operator)
	})

	It("keeps the deleted subscription and retries the create while the old one is terminating", func() {
		sub := subscription(installPlanFailed)
		kc.EXPECT().GetSubscription(key).Return(sub, nil).Times(1)
		kc.EXPECT().DeleteSubscription(key).Return(nil).Times(1)
		kc.EXPECT().GetCSV(namespace, csvName).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().DeleteCSV(namespace, csvName).Return(nil).Times(1)
		kc.EXPECT().CreateSubscription(gomock.Any()).Return(alreadyExists).Times(1)

		c.recoverWedgedSubscription(operator)
		Expect(c.subscriptionRecovery[key].pending).NotTo(BeNil())

		// Next poll: only the create is retried, the subscription is not re-read
		// (it is gone, or is the old one still terminating).
		kc.EXPECT().CreateSubscription(gomock.Any()).DoAndReturn(func(created *olmv1alpha1.Subscription) error {
			Expect(created.Spec).To(Equal(sub.Spec))
			return nil
		}).Times(1)

		c.recoverWedgedSubscription(operator)
		Expect(c.subscriptionRecovery[key].pending).To(BeNil())
		Expect(c.subscriptionRecovery[key].attempts).To(Equal(1))
	})

	It("does not delete the CSV or create anything when deleting the subscription fails", func() {
		kc.EXPECT().GetSubscription(key).Return(subscription(installPlanFailed), nil).Times(1)
		kc.EXPECT().DeleteSubscription(key).Return(fmt.Errorf("forbidden")).Times(1)
		kc.EXPECT().DeleteCSV(gomock.Any(), gomock.Any()).Times(0)
		kc.EXPECT().CreateSubscription(gomock.Any()).Times(0)

		c.recoverWedgedSubscription(operator)

		state := c.subscriptionRecovery[key]
		Expect(state.pending).To(BeNil())
		// The attempt still counts, so a persistent delete failure is also backed off.
		Expect(state.attempts).To(Equal(1))
	})

	It("is only run by the agent-based installer, and only for operators that are not installed", func() {
		ops := []models.MonitoredOperator{operator}
		kc.EXPECT().GetCSVFromSubscription(namespace, subName).Return(csvName, nil).AnyTimes()

		// Not installed, agent-based installer: the recovery check runs.
		kc.EXPECT().GetCSV(namespace, csvName).Return(csvInPhase(olmv1alpha1.CSVPhasePending), nil).Times(1)
		kc.EXPECT().GetSubscription(key).Return(subscription(), nil).Times(1)
		ready, _, err := c.getReadyOperators(ops, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeEmpty())

		// Installed: no recovery check.
		kc.EXPECT().GetCSV(namespace, csvName).Return(csvInPhase(olmv1alpha1.CSVPhaseSucceeded), nil).Times(1)
		ready, _, err = c.getReadyOperators(ops, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(ConsistOf("loki"))
	})
})
