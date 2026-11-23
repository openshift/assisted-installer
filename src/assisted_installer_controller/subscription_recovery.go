package assisted_installer_controller

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/openshift/assisted-service/models"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// maxSubscriptionRecoveries caps how many times a single subscription is recreated.
	// If its InstallPlan keeps failing after this many attempts the cause is not
	// transient and needs a human.
	maxSubscriptionRecoveries = 5

	// subscriptionRecoveryBackoff is the minimum time between two recreations of the same
	// subscription. A recreated subscription needs time for OLM to resolve it, generate a
	// new InstallPlan and run it, and whatever failed the previous plan (for example a CRD
	// validating webhook whose backend is not serving yet) needs time to clear.
	subscriptionRecoveryBackoff = 5 * time.Minute
)

// subscriptionRecoveryState tracks recovery of one subscription across polls. The
// ClusterServiceVersionHandler is rebuilt on every poll, so this lives on the controller.
type subscriptionRecoveryState struct {
	attempts    int
	lastAttempt time.Time
	// pending holds a subscription that has been deleted but not yet successfully
	// recreated. While it is set, the next poll retries the create before doing
	// anything else, so a failed create never loses the subscription.
	pending *olmv1alpha1.Subscription
}

// subscriptionWedged reports whether OLM will never finish installing the
// subscription on its own, and why.
//
// OLM treats a Failed InstallPlan as terminal and does not retry it, and once the
// InstallPlan referenced from status.installPlanRef has been deleted it does not
// generate a replacement either. In both cases the CSV that the plan created in its
// first step is left in Pending/RequirementsNotMet and the operator's CRDs are never
// registered.
func subscriptionWedged(sub *olmv1alpha1.Subscription) (string, bool) {
	for _, cond := range sub.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case olmv1alpha1.SubscriptionInstallPlanFailed, olmv1alpha1.SubscriptionInstallPlanMissing:
			return fmt.Sprintf("%s/%s: %s", cond.Type, cond.Reason, cond.Message), true
		}
	}
	return "", false
}

// recoverWedgedSubscription recreates an operator's subscription, and removes the CSV
// stranded by its failed InstallPlan, when OLM will not finish the install on its own.
// See subscriptionWedged.
//
// This is the documented manual recovery for a failed OLM install, done automatically.
// It is used by the agent-based installer only, where InstallPlans routinely fail
// during the first minutes after the control plane comes up. Recreations of a given
// subscription are spaced by subscriptionRecoveryBackoff and capped at
// maxSubscriptionRecoveries.
//
// Only called from the PostInstallConfigsK8sClient goroutine, so subscriptionRecovery
// needs no locking.
func (c *controller) recoverWedgedSubscription(operator models.MonitoredOperator) {
	key := types.NamespacedName{Namespace: operator.Namespace, Name: operator.SubscriptionName}
	state, ok := c.subscriptionRecovery[key]
	if !ok {
		state = &subscriptionRecoveryState{}
		c.subscriptionRecovery[key] = state
	}

	// Finish a recreation that an earlier poll started.
	if state.pending != nil {
		c.createRecoveredSubscription(key, state)
		return
	}

	sub, err := c.kc.GetSubscription(key)
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get subscription %s to check whether it needs recovery", key)
		return
	}

	reason, wedged := subscriptionWedged(sub)
	if !wedged {
		return
	}

	if state.attempts >= maxSubscriptionRecoveries {
		c.log.Warnf("Subscription %s for operator %s is still wedged (%s) after %d recreations, giving up",
			key, operator.Name, reason, state.attempts)
		return
	}
	if since := time.Since(state.lastAttempt); since < subscriptionRecoveryBackoff {
		c.log.Infof("Subscription %s for operator %s is wedged (%s), next recreation in %s",
			key, operator.Name, reason, (subscriptionRecoveryBackoff - since).Round(time.Second))
		return
	}

	state.attempts++
	state.lastAttempt = time.Now()
	c.log.Warnf("Subscription %s for operator %s is wedged (%s), recreating it (attempt %d/%d)",
		key, operator.Name, reason, state.attempts, maxSubscriptionRecoveries)

	recreated := subscriptionForRecreate(sub)
	// Log the full object first, so it can be recreated by hand if the controller
	// dies between the delete and the create below.
	if raw, err := json.Marshal(recreated); err == nil {
		c.log.Infof("Subscription %s to be recreated: %s", key, string(raw))
	}

	if err := c.kc.DeleteSubscription(key); err != nil && !apierrors.IsNotFound(err) {
		c.log.WithError(err).Warnf("Failed to delete subscription %s", key)
		return
	}
	state.pending = recreated

	// Without its subscription nothing will ever move the stranded CSV out of Pending,
	// and leaving it in place lets the recreated subscription adopt it instead of
	// running a fresh InstallPlan.
	for _, csvName := range strandedCSVs(sub) {
		c.deleteStrandedCSV(key.Namespace, csvName)
	}

	c.createRecoveredSubscription(key, state)
}

// createRecoveredSubscription creates state.pending and clears it on success. On
// failure it is left set and retried on the next poll; AlreadyExists is expected
// while the deleted subscription is still being removed.
func (c *controller) createRecoveredSubscription(key types.NamespacedName, state *subscriptionRecoveryState) {
	err := c.kc.CreateSubscription(state.pending.DeepCopy())
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			c.log.Infof("Subscription %s is still being deleted, will recreate it on the next check", key)
		} else {
			c.log.WithError(err).Warnf("Failed to recreate subscription %s, will retry", key)
		}
		return
	}
	c.log.Infof("Recreated subscription %s", key)
	state.pending = nil
}

// deleteStrandedCSV deletes a CSV left behind by a failed InstallPlan. A CSV that
// reached Succeeded is never deleted.
func (c *controller) deleteStrandedCSV(namespace, name string) {
	csv, err := c.kc.GetCSV(namespace, name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			c.log.WithError(err).Warnf("Failed to get CSV %s/%s", namespace, name)
		}
		return
	}
	if csv.Status.Phase == olmv1alpha1.CSVPhaseSucceeded {
		return
	}
	c.log.Infof("Deleting CSV %s/%s stranded in phase %s", namespace, name, csv.Status.Phase)
	if err := c.kc.DeleteCSV(namespace, name); err != nil && !apierrors.IsNotFound(err) {
		c.log.WithError(err).Warnf("Failed to delete CSV %s/%s", namespace, name)
	}
}

// strandedCSVs returns the distinct CSV names a subscription points at.
func strandedCSVs(sub *olmv1alpha1.Subscription) []string {
	var names []string
	for _, name := range []string{sub.Status.CurrentCSV, sub.Status.InstalledCSV} {
		if name != "" && (len(names) == 0 || names[0] != name) {
			names = append(names, name)
		}
	}
	return names
}

// subscriptionForRecreate returns a copy of sub that can be passed to Create: the same
// name, namespace, labels, annotations and spec, without status or server-populated
// metadata.
func subscriptionForRecreate(sub *olmv1alpha1.Subscription) *olmv1alpha1.Subscription {
	cp := sub.DeepCopy()
	return &olmv1alpha1.Subscription{
		TypeMeta: cp.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:        cp.Name,
			Namespace:   cp.Namespace,
			Labels:      cp.Labels,
			Annotations: cp.Annotations,
		},
		Spec: cp.Spec,
	}
}
