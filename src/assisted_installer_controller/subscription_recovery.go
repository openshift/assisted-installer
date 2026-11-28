package assisted_installer_controller

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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

	// deletedPlanWait bounds how long a recreation waits for the deleted InstallPlan to
	// disappear. A plan that is still there after this is not going away on its own (for
	// example a finalizer), and the subscription must not stay deleted because of it.
	deletedPlanWait = 2 * time.Minute

	// operatorLabelPrefix prefixes the label OLM puts on an operator's subscription and on
	// the CSVs it creates for it: operators.coreos.com/<package>.<namespace>.
	operatorLabelPrefix = "operators.coreos.com/"

	// copiedCSVLabel marks a CSV that OLM copied into this namespace from an operator
	// installed for all namespaces. It belongs to that other operator and is never ours to delete.
	copiedCSVLabel = "olm.copiedFrom"
)

// subscriptionRecoveryState tracks recovery of one subscription across polls. The
// controller is the only long-lived object in the poll loop, so this lives on it.
type subscriptionRecoveryState struct {
	attempts    int
	lastAttempt time.Time
	// pending holds a subscription that has been deleted but not yet successfully
	// recreated. While it is set, every poll retries the create first, so a failed
	// create never loses the subscription.
	pending *olmv1alpha1.Subscription
	// deletedPlan is the InstallPlan deleted together with pending. pending is only
	// created once that plan is gone; see recoverWedgedGroup.
	deletedPlan *types.NamespacedName
	// installed is set once a recreated subscription's CSV has reached Succeeded.
	installed bool
	// gaveUp is set once the subscription is still wedged after maxSubscriptionRecoveries.
	gaveUp bool
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

// wedgedGroup is a set of subscriptions that must be recovered together because they
// reference the same InstallPlan.
type wedgedGroup struct {
	// plan is the InstallPlan the subscriptions reference, nil if they reference none.
	plan *types.NamespacedName
	subs []*olmv1alpha1.Subscription
}

// recoverWedgedSubscriptions recreates every wedged subscription in the cluster, and
// removes the CSVs stranded by their failed InstallPlans, when OLM will not finish the
// install on its own. See subscriptionWedged. It returns true while any recovery is
// still outstanding: a recreate that has not been created yet, a subscription that is
// wedged and has attempts left, or a recreated subscription whose CSV has not reached
// Succeeded yet.
//
// This is the documented manual recovery for a failed OLM install, done automatically,
// for the agent-based installer only, where InstallPlans routinely fail during the first
// minutes after the control plane comes up. It covers every subscription, not only the
// operators the controller monitors, because the agent-based installer creates other
// Day-1 operators too (for example fence-agents-remediation and node-maintenance) and
// they wedge the same way.
//
// OLM resolves all subscriptions of a namespace together and gives them one InstallPlan,
// so a failed plan is shared by every subscription that references it. Those
// subscriptions are recovered as one group: all of them are deleted, then the failed
// plan, then all of them are recreated. Recreating only some of them lets OLM resolve the
// same set of CSVs again and reuse the failed plan, so the recreated subscription fails
// again at once. Deleting the plan is safe only because no subscription references it
// any more; deleting a plan that a subscription still references is exactly what wedges
// it (see IsInstalled).
//
// Recreations of a given subscription are spaced by subscriptionRecoveryBackoff and capped
// at maxSubscriptionRecoveries. Only called from the PostInstallConfigsK8sClient
// goroutine, so subscriptionRecovery needs no locking.
func (c *controller) recoverWedgedSubscriptions() bool {
	outstanding := false

	// Finish recreations that an earlier poll started.
	for _, key := range c.sortedRecoveryKeys() {
		state := c.subscriptionRecovery[key]
		if state.pending == nil {
			continue
		}
		if !c.deletedPlanGone(key, state) {
			outstanding = true
			continue
		}
		c.createRecoveredSubscription(key, state)
		if state.pending != nil {
			outstanding = true
		}
	}

	subs, err := c.kc.ListSubscriptions(metav1.NamespaceAll)
	if err != nil {
		c.log.WithError(err).Warnf("Failed to list subscriptions to check whether any needs recovery")
		return true
	}

	if c.checkRecoveredSubscriptions(subs) {
		outstanding = true
	}

	for _, group := range c.wedgedGroups(subs) {
		if c.recoverWedgedGroup(group) {
			outstanding = true
		}
	}
	return outstanding
}

// wedgedGroups groups the wedged subscriptions by the InstallPlan they reference. A group
// is left out while any subscription referencing its plan is not wedged yet, since the
// plan cannot be deleted while it is still referenced; OLM sets the condition on the
// others shortly.
func (c *controller) wedgedGroups(subs []olmv1alpha1.Subscription) []wedgedGroup {
	byPlan := map[types.NamespacedName][]*olmv1alpha1.Subscription{}
	var planOrder []types.NamespacedName
	var groups []wedgedGroup

	for i := range subs {
		sub := &subs[i]
		key := types.NamespacedName{Namespace: sub.Namespace, Name: sub.Name}
		if sub.DeletionTimestamp != nil {
			continue
		}
		if state, ok := c.subscriptionRecovery[key]; ok && state.pending != nil {
			continue
		}
		ref := sub.Status.InstallPlanRef
		if ref == nil {
			if _, wedged := subscriptionWedged(sub); wedged {
				groups = append(groups, wedgedGroup{subs: []*olmv1alpha1.Subscription{sub}})
			}
			continue
		}
		plan := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
		if plan.Namespace == "" {
			plan.Namespace = sub.Namespace
		}
		if _, seen := byPlan[plan]; !seen {
			planOrder = append(planOrder, plan)
		}
		byPlan[plan] = append(byPlan[plan], sub)
	}

	for _, plan := range planOrder {
		members := byPlan[plan]
		var wedged, notWedged []string
		for _, sub := range members {
			if _, ok := subscriptionWedged(sub); ok {
				wedged = append(wedged, sub.Namespace+"/"+sub.Name)
			} else {
				notWedged = append(notWedged, sub.Namespace+"/"+sub.Name)
			}
		}
		if len(wedged) == 0 {
			continue
		}
		if len(notWedged) > 0 {
			c.log.Infof("Subscriptions %v are wedged on InstallPlan %s, waiting for %v, which share it, before recovering them together",
				wedged, plan, notWedged)
			continue
		}
		p := plan
		groups = append(groups, wedgedGroup{plan: &p, subs: members})
	}
	return groups
}

// recoverWedgedGroup recreates one group of wedged subscriptions. It returns true while
// the group's recovery is outstanding.
func (c *controller) recoverWedgedGroup(group wedgedGroup) bool {
	states := make([]*subscriptionRecoveryState, len(group.subs))
	for i, sub := range group.subs {
		key := types.NamespacedName{Namespace: sub.Namespace, Name: sub.Name}
		state, ok := c.subscriptionRecovery[key]
		if !ok {
			state = &subscriptionRecoveryState{}
			c.subscriptionRecovery[key] = state
		}
		states[i] = state
	}

	for i, sub := range group.subs {
		reason, _ := subscriptionWedged(sub)
		if states[i].attempts >= maxSubscriptionRecoveries {
			if !states[i].gaveUp {
				c.log.Warnf("Subscription %s/%s is still wedged (%s) after %d recreations, giving up",
					sub.Namespace, sub.Name, reason, states[i].attempts)
				states[i].gaveUp = true
			}
			return false
		}
		if since := time.Since(states[i].lastAttempt); since < subscriptionRecoveryBackoff {
			c.log.Infof("Subscription %s/%s is wedged (%s), next recreation in %s",
				sub.Namespace, sub.Name, reason, (subscriptionRecoveryBackoff - since).Round(time.Second))
			return true
		}
	}

	allDeleted := true
	var deleted []int
	for i, sub := range group.subs {
		key := types.NamespacedName{Namespace: sub.Namespace, Name: sub.Name}
		reason, _ := subscriptionWedged(sub)
		state := states[i]
		state.attempts++
		state.lastAttempt = time.Now()
		state.installed = false
		c.log.Warnf("Subscription %s is wedged (%s), recreating it (attempt %d/%d)",
			key, reason, state.attempts, maxSubscriptionRecoveries)

		recreated := subscriptionForRecreate(sub)
		// Log the full object first, so it can be recreated by hand if the controller
		// dies between the delete and the create below.
		if raw, err := json.Marshal(recreated); err == nil {
			c.log.Infof("Subscription %s to be recreated: %s", key, string(raw))
		}

		if err := c.kc.DeleteSubscription(key); err != nil && !apierrors.IsNotFound(err) {
			c.log.WithError(err).Warnf("Failed to delete subscription %s", key)
			allDeleted = false
			continue
		}
		state.pending = recreated
		deleted = append(deleted, i)
	}

	// Without its subscription nothing will ever move a stranded CSV out of Pending,
	// and leaving it in place lets the recreated subscription adopt it instead of
	// running a fresh InstallPlan.
	for _, i := range deleted {
		for _, csvName := range c.strandedCSVs(group.subs[i]) {
			c.deleteStrandedCSV(group.subs[i].Namespace, csvName)
		}
	}

	// Only once nothing references it, so OLM cannot reuse the failed plan.
	var deletedPlan *types.NamespacedName
	if group.plan != nil && allDeleted {
		err := c.kc.DeleteInstallPlan(*group.plan)
		switch {
		case err == nil:
			c.log.Infof("Deleted InstallPlan %s, which no subscription references any more", *group.plan)
			deletedPlan = group.plan
		case apierrors.IsNotFound(err):
			c.log.Infof("InstallPlan %s is already gone", *group.plan)
		default:
			c.log.WithError(err).Warnf("Failed to delete InstallPlan %s, recreating its subscriptions anyway", *group.plan)
		}
	}

	// The subscriptions are recreated on a later poll, not straight away. OLM resolves a
	// new subscription against its own cached view of the namespace; if that view still
	// has the plan deleted a moment ago, OLM reuses it, and the recreated subscription
	// reports InstallPlanMissing at once (seen with mtv, which then needed a whole
	// backoff round). Waiting until the plan is confirmed gone, and at least one poll,
	// gives OLM time to see the delete.
	for _, i := range deleted {
		sub := group.subs[i]
		states[i].deletedPlan = deletedPlan
		c.log.Infof("Subscription %s/%s will be recreated on the next check", sub.Namespace, sub.Name)
	}
	return true
}

// deletedPlanGone reports whether the InstallPlan deleted together with state.pending
// is gone, so the subscription can be recreated. It gives up waiting after
// deletedPlanWait, so a plan that never disappears cannot leave the subscription deleted.
func (c *controller) deletedPlanGone(key types.NamespacedName, state *subscriptionRecoveryState) bool {
	if state.deletedPlan == nil {
		return true
	}
	plan := *state.deletedPlan
	_, err := c.kc.GetInstallPlan(plan)
	switch {
	case apierrors.IsNotFound(err):
		state.deletedPlan = nil
		return true
	case time.Since(state.lastAttempt) >= deletedPlanWait:
		c.log.Warnf("InstallPlan %s is still present %s after it was deleted, recreating subscription %s anyway",
			plan, deletedPlanWait, key)
		state.deletedPlan = nil
		return true
	case err != nil:
		c.log.WithError(err).Infof("Failed to check whether InstallPlan %s is gone, will recreate subscription %s on a later check", plan, key)
		return false
	default:
		c.log.Infof("InstallPlan %s is still being deleted, will recreate subscription %s on a later check", plan, key)
		return false
	}
}

// checkRecoveredSubscriptions reports whether any subscription that was recreated is
// still on its way to a Succeeded CSV, and marks the ones that got there.
func (c *controller) checkRecoveredSubscriptions(subs []olmv1alpha1.Subscription) bool {
	outstanding := false
	for i := range subs {
		sub := &subs[i]
		key := types.NamespacedName{Namespace: sub.Namespace, Name: sub.Name}
		state, ok := c.subscriptionRecovery[key]
		if !ok || state.attempts == 0 || state.installed || state.gaveUp || state.pending != nil {
			continue
		}
		if c.csvSucceeded(sub) {
			c.log.Infof("Recreated subscription %s installed %s", key, sub.Status.CurrentCSV)
			state.installed = true
			continue
		}
		outstanding = true
	}
	return outstanding
}

func (c *controller) csvSucceeded(sub *olmv1alpha1.Subscription) bool {
	if sub.Status.CurrentCSV == "" {
		return false
	}
	csv, err := c.kc.GetCSV(sub.Namespace, sub.Status.CurrentCSV)
	if err != nil {
		return false
	}
	return csv.Status.Phase == olmv1alpha1.CSVPhaseSucceeded
}

func (c *controller) sortedRecoveryKeys() []types.NamespacedName {
	keys := make([]types.NamespacedName, 0, len(c.subscriptionRecovery))
	for key := range c.subscriptionRecovery {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}

// createRecoveredSubscription creates state.pending and clears it on success. On
// failure it is left set and retried on the next poll.
func (c *controller) createRecoveredSubscription(key types.NamespacedName, state *subscriptionRecoveryState) {
	err := c.kc.CreateSubscription(state.pending.DeepCopy())
	if err == nil {
		c.log.Infof("Recreated subscription %s", key)
		c.finishRecovery(state)
		return
	}
	if !apierrors.IsAlreadyExists(err) {
		c.log.WithError(err).Warnf("Failed to recreate subscription %s, will retry", key)
		return
	}

	// The delete of the original already succeeded, so an existing subscription is
	// either the original still terminating or a replacement: ours, from an earlier
	// create whose response was lost, or one created by someone else. Only keep
	// retrying for the former; a replacement must go through the normal wedge check,
	// otherwise a replacement that wedges again would never be noticed.
	existing, getErr := c.kc.GetSubscription(key)
	if getErr != nil {
		c.log.WithError(getErr).Infof("Subscription %s already exists but could not be read, will retry", key)
		return
	}
	if existing.DeletionTimestamp != nil {
		c.log.Infof("Subscription %s is still being deleted, will recreate it on the next check", key)
		return
	}
	c.log.Infof("Subscription %s has already been recreated", key)
	c.finishRecovery(state)
}

// finishRecovery records that the subscription exists again. The backoff restarts
// here rather than from the delete, so a recreation whose create was delayed still
// gets the full subscriptionRecoveryBackoff for OLM to run its new InstallPlan.
func (c *controller) finishRecovery(state *subscriptionRecoveryState) {
	state.pending = nil
	state.lastAttempt = time.Now()
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

// strandedCSVs returns the distinct names of the CSVs that may have been left behind
// for a subscription: the ones its status points at, and every CSV OLM created for
// the same operator in its namespace.
//
// The status alone is not enough. When OLM moves currentCSV on to a newer version
// before the first CSV installs (for example v2.12.10, then v5.0.0 from the default
// channel), the older CSV is no longer referenced but is still there. After the
// subscription is recreated OLM then refuses to resolve it, with ResolutionFailed
// "clusterserviceversion ... exists and is not referenced by a subscription", and the
// operator never installs.
//
// deleteStrandedCSV still checks each one and never deletes a Succeeded CSV.
func (c *controller) strandedCSVs(sub *olmv1alpha1.Subscription) []string {
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	add(sub.Status.CurrentCSV)
	add(sub.Status.InstalledCSV)

	label := operatorLabel(sub)
	if label == "" {
		return names
	}
	csvs, err := c.kc.ListCSVs(sub.Namespace, label)
	if err != nil {
		c.log.WithError(err).Warnf("Failed to list CSVs of subscription %s/%s, only checking the ones it references",
			sub.Namespace, sub.Name)
		return names
	}
	for _, csv := range csvs {
		if _, copied := csv.Labels[copiedCSVLabel]; copied {
			continue
		}
		add(csv.Name)
	}
	return names
}

// operatorLabel returns the operators.coreos.com/<package>.<namespace> label key OLM
// uses for the subscription's operator. OLM sets it on the subscription itself, so
// that copy is used when present; otherwise it is built from the package name.
func operatorLabel(sub *olmv1alpha1.Subscription) string {
	for k := range sub.Labels {
		if strings.HasPrefix(k, operatorLabelPrefix) {
			return k
		}
	}
	if sub.Spec == nil || sub.Spec.Package == "" {
		return ""
	}
	return operatorLabelPrefix + sub.Spec.Package + "." + sub.Namespace
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
