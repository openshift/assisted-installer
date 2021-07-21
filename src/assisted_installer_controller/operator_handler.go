package assisted_installer_controller

import (
	"context"
	"time"

	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
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

func (c controller) isOperatorAvailable(handler OperatorHandler) bool {
	operatorName := handler.GetName()
	c.log.Infof("Checking <%s> operator availability status", operatorName)

	operatorStatusInService, isAvailable := c.isOperatorAvailableInService(operatorName)
	if isAvailable {
		return true
	}

	operatorStatus, operatorMessage, err := handler.GetStatus()
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get <%s> operator", operatorName)
		return false
	}

	if operatorStatusInService.Status != operatorStatus || (operatorStatusInService.StatusInfo != operatorMessage && operatorMessage != "") {
		c.log.Infof("Operator <%s> updated, status: %s -> %s, message: %s -> %s.", operatorName, operatorStatusInService.Status, operatorStatus, operatorStatusInService.StatusInfo, operatorMessage)
		if !handler.OnChange(operatorStatus) {
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

func (c controller) isOperatorAvailableInService(operatorName string) (*models.MonitoredOperator, bool) {
	operatorStatusInService, err := c.ic.GetClusterMonitoredOperator(utils.GenerateRequestContext(), c.ClusterID, operatorName)
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

type ClusterServiceVersionHandler struct {
	kc       k8s_client.K8SClient
	operator *models.MonitoredOperator
	status   *ControllerStatus
	retries  int
}

func NewClusterServiceVersionHandler(kc k8s_client.K8SClient, operator *models.MonitoredOperator, status *ControllerStatus) *ClusterServiceVersionHandler {
	return &ClusterServiceVersionHandler{kc: kc, operator: operator, status: status, retries: 0}
}

func (handler ClusterServiceVersionHandler) GetName() string { return handler.operator.Name }

func (handler ClusterServiceVersionHandler) GetStatus() (models.OperatorStatus, string, error) {
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

func (handler ClusterServiceVersionHandler) OnChange(newStatus models.OperatorStatus) bool {
	if utils.IsStatusFailed(newStatus) {
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
