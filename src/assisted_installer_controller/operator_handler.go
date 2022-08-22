package assisted_installer_controller

import (
	"context"
	"time"

	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	cvoOperatorName    = "cvo"
	clusterVersionName = "version"
)

type OperatorHandler interface {
	GetName() string
	GetStatus() (models.OperatorStatus, string, error)
	OnChange(newStatus models.OperatorStatus) bool
}

func (c controller) isOperatorAvailable(operatorHandler OperatorHandler) bool {
	operatorName := operatorHandler.GetName()
	c.log.Infof("Checking <%s> operator availability status", operatorName)

	operatorStatusInService, isAvailable := c.isOperatorAvailableInService(operatorName, c.OpenshiftVersion)
	if isAvailable {
		return true
	}

	operatorStatus, operatorMessage, err := operatorHandler.GetStatus()
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get <%s> operator", operatorName)
		return false
	}

	if operatorStatusInService.Status != operatorStatus || (operatorStatusInService.StatusInfo != operatorMessage && operatorMessage != "") {
		c.log.Infof("Operator <%s> updated, status: %s -> %s, message: %s -> %s.", operatorName, operatorStatusInService.Status, operatorStatus, operatorStatusInService.StatusInfo, operatorMessage)
		if !operatorHandler.OnChange(operatorStatus) {
			c.log.WithError(err).Warnf("<%s> operator's OnChange() returned false. Will skip an update.", operatorName)
			return false
		}

		err = c.ic.UpdateClusterOperator(context.TODO(), c.ClusterID, operatorName, operatorStatus, operatorMessage)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to update %s operator status %s with message %s", operatorName, operatorStatus, operatorMessage)
			return false
		}
	}

	return false
}

func (c controller) isOperatorAvailableInService(operatorName string, openshiftVersion string) (*models.MonitoredOperator, bool) {
	operatorStatusInService, err := c.ic.GetClusterMonitoredOperator(utils.GenerateRequestContext(), c.ClusterID, operatorName, openshiftVersion)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to get cluster %s %s operator status", c.ClusterID, operatorName)
		return nil, false
	}

	if operatorStatusInService.Status == models.OperatorStatusAvailable {
		c.log.Infof("Service acknowledged <%s> operator is available for cluster %s", operatorName, c.ClusterID)
		return operatorStatusInService, true
	}

	return operatorStatusInService, false
}

type ClusterOperatorHandler struct {
	kc           k8s_client.K8SClient
	operatorName string
}

func NewClusterOperatorHandler(kc k8s_client.K8SClient, operatorName string) *ClusterOperatorHandler {
	return &ClusterOperatorHandler{kc: kc, operatorName: operatorName}
}

func (handler ClusterOperatorHandler) GetName() string { return handler.operatorName }

func (handler ClusterOperatorHandler) GetStatus() (models.OperatorStatus, string, error) {
	co, err := handler.kc.GetClusterOperator(handler.operatorName)
	if err != nil {
		return "", "", err
	}

	operatorStatus, operatorMessage := utils.ClusterOperatorConditionsToMonitoredOperatorStatus(co.Status.Conditions)
	return operatorStatus, operatorMessage, nil
}

func (handler ClusterOperatorHandler) OnChange(_ models.OperatorStatus) bool { return true }

type ClusterVersionHandler struct {
	kc    k8s_client.K8SClient
	timer *time.Timer
}

func NewClusterVersionHandler(kc k8s_client.K8SClient, timer *time.Timer) *ClusterVersionHandler {
	return &ClusterVersionHandler{kc: kc, timer: timer}
}

func (handler ClusterVersionHandler) GetName() string { return cvoOperatorName }

func (handler ClusterVersionHandler) GetStatus() (models.OperatorStatus, string, error) {
	co, err := handler.kc.GetClusterVersion(clusterVersionName)
	if err != nil {
		return "", "", err
	}

	operatorStatus, operatorMessage := utils.ClusterOperatorConditionsToMonitoredOperatorStatus(co.Status.Conditions)
	return operatorStatus, operatorMessage, nil
}

func (handler ClusterVersionHandler) OnChange(_ models.OperatorStatus) bool {
	// This is a common pattern to ensure the channel is empty after a stop has been called
	// More info on time/sleep.go documentation
	if !handler.timer.Stop() {
		<-handler.timer.C
	}
	handler.timer.Reset(WaitTimeout)

	return true
}

type OLMOperatorHandler struct {
	kc       k8s_client.K8SClient
	operator *models.MonitoredOperator
	status   *ControllerStatus
	retries  int
}

func NewOLMOperatorHandler(kc k8s_client.K8SClient, operator *models.MonitoredOperator, status *ControllerStatus) *OLMOperatorHandler {
	return &OLMOperatorHandler{kc: kc, operator: operator, status: status, retries: 0}
}

func (handler OLMOperatorHandler) GetName() string { return handler.operator.Name }

func (handler OLMOperatorHandler) HasCSVBeenCreated() bool {
	csvName, err := handler.kc.GetCSVFromSubscription(handler.operator.Namespace, handler.operator.SubscriptionName)
	if err != nil {
		return false
	}

	if _, err := handler.kc.GetCSV(handler.operator.Namespace, csvName); err != nil {
		_ = handler.handleOLMEarlySetupBug()
		return false
	}

	return true
}

// handleOLMEarlySetupBug converts all manual subscriptions created by the
// Assisted Service into automatic ones. Some versions of the Assisted Service
// apply the subscription as manual (as opposed to automatic), expecting the
// Assisted Controller to take care of it once the cluster is up. The service
// does this because subscriptions created too early in the installation
// process might fail operator installation, and OLM will not retry installing
// them. That's why we prefer to initially create them as manual and only once
// the cluster is at a later stage of the installation and our Assisted
// Controller is up, we convert them to automatic to trigger their installation
// which at this point is more likely to succeed.
func (handler OLMOperatorHandler) handleOLMEarlySetupBug() error {
	isManual, err := handler.IsSubscriptionManual()
	if err != nil {
		return err
	}

	if isManual {
		err := handler.kc.MakeSubscriptionAutomatic(types.NamespacedName{
			Name:      handler.operator.SubscriptionName,
			Namespace: handler.operator.Namespace,
		})
		if err != nil {
			return err
		}

		// OLM for some reason doesn't reconcile the subscription becoming
		// automatic, so we have to delete all the install plans to force OLM
		// to re-create them as automatic install plans
		err = handler.deleteSubscriptionInstallPlans()
		if err != nil {
			return err
		}
	}

	return nil
}

func (handler OLMOperatorHandler) deleteSubscriptionInstallPlans() error {
	subscriptionInstallPlans, err := handler.kc.GetAllInstallPlansOfSubscription(types.NamespacedName{
		Name:      handler.operator.SubscriptionName,
		Namespace: handler.operator.Namespace,
	})
	if err != nil {
		return err
	}

	for _, installPlan := range subscriptionInstallPlans {
		if err := handler.kc.DeleteInstallPlan(types.NamespacedName{
			Name:      installPlan.Name,
			Namespace: installPlan.Namespace,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (handler OLMOperatorHandler) IsSubscriptionManual() (bool, error) {
	subscription, err := handler.kc.GetSubscription(types.NamespacedName{
		Namespace: handler.operator.Namespace,
		Name:      handler.operator.SubscriptionName,
	})
	if err != nil {
		return false, err
	}

	return subscription.Spec.InstallPlanApproval == olmv1alpha1.ApprovalManual, nil
}

func (handler OLMOperatorHandler) GetStatus() (models.OperatorStatus, string, error) {
	csvName, err := handler.kc.GetCSVFromSubscription(handler.operator.Namespace, handler.operator.SubscriptionName)
	if err != nil {
		return "", "", err
	}

	csv, err := handler.kc.GetCSV(handler.operator.Namespace, csvName)
	if err != nil {
		return "", "", err
	}
	operatorStatus := utils.CsvStatusToOperatorStatus(string(csv.Status.Phase))
	return operatorStatus, csv.Status.Message, nil
}

func (handler OLMOperatorHandler) OnChange(newStatus models.OperatorStatus) bool {
	if IsStatusFailed(newStatus) {
		if handler.retries < failedOperatorRetry {
			// FIXME: We retry the check of the operator status in case it's in failed state to WA bug 1968606
			// Remove this code when bug 1968606 is fixed
			handler.retries++
			return false
		}
		handler.status.OperatorError(handler.operator.Name)
	}

	return true
}

func IsStatusFailed(operatorStatus models.OperatorStatus) bool {
	return operatorStatus == models.OperatorStatusFailed
}

func IsStatusSucceeded(operatorStatus models.OperatorStatus) bool {
	return operatorStatus == models.OperatorStatusAvailable
}
