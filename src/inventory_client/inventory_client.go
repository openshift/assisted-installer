package inventory_client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/PuerkitoBio/rehttp"
	ttlCache "github.com/ReneKroon/ttlcache/v2"
	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	"github.com/hashicorp/go-version"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/client"
	"github.com/openshift/assisted-service/client/events"
	"github.com/openshift/assisted-service/client/installer"
	"github.com/openshift/assisted-service/client/operators"
	"github.com/openshift/assisted-service/models"
	"github.com/openshift/assisted-service/pkg/auth"
	aserror "github.com/openshift/assisted-service/pkg/error"
	"github.com/openshift/assisted-service/pkg/requestid"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
)

const (
	DefaultRetryMinDelay = time.Duration(2) * time.Second
	DefaultRetryMaxDelay = time.Duration(10) * time.Second
	DefaultMinRetries    = 10
	DefaultMaxRetries    = 360
)

//go:generate mockgen -source=inventory_client.go -package=inventory_client -destination=mock_inventory_client.go
type InventoryClient interface {
	DownloadFile(ctx context.Context, filename string, dest string) error
	DownloadClusterCredentials(ctx context.Context, filename string, dest string) error
	DownloadHostIgnition(ctx context.Context, infraEnvID string, hostID string, dest string) error
	UpdateHostInstallProgress(ctx context.Context, infraEnvId string, hostId string, newStage models.HostStage, info string) error
	GetEnabledHostsNamesHosts(ctx context.Context, log logrus.FieldLogger) (map[string]HostData, error)
	UploadIngressCa(ctx context.Context, ingressCA string, clusterId string) error
	GetCluster(ctx context.Context, withHosts bool) (*models.Cluster, error)
	ListsHostsForRole(ctx context.Context, role string) (models.HostList, error)
	GetClusterMonitoredOperator(ctx context.Context, clusterId, operatorName string, openshiftVersion string) (*models.MonitoredOperator, error)
	GetClusterMonitoredOLMOperators(ctx context.Context, clusterId string, openshiftVersion string) ([]models.MonitoredOperator, error)
	CompleteInstallation(ctx context.Context, clusterId string, isSuccess bool, errorInfo string, data map[string]interface{}) error
	GetHosts(ctx context.Context, log logrus.FieldLogger, skippedStatuses []string) (map[string]HostData, error)
	UploadLogs(ctx context.Context, clusterId string, logsType models.LogsType, upfile io.Reader) error
	ClusterLogProgressReport(ctx context.Context, clusterId string, progress models.LogsState)
	HostLogProgressReport(ctx context.Context, infraEnvId string, hostId string, progress models.LogsState)
	UpdateClusterOperator(ctx context.Context, clusterId string, operatorName string, operatorVersion string, operatorStatus models.OperatorStatus, operatorStatusInfo string) error
	TriggerEvent(ctx context.Context, ev *models.Event) error
	UpdateFinalizingProgress(ctx context.Context, clusterId string, stage models.FinalizingStage) error
}

type inventoryClient struct {
	ai        *client.AssistedInstall
	clusterId strfmt.UUID
	logger    logrus.FieldLogger
	cache     ttlCache.SimpleCache
}

type HostData struct {
	IPs       []string
	Inventory *models.Inventory
	Host      *models.Host
}

func CreateInventoryClientWithDelay(clusterId string, inventoryURL string, pullSecret string, insecure bool, caPath string,
	logger logrus.FieldLogger, proxyFunc func(*http.Request) (*url.URL, error),
	retryMinDelay, retryMaxDelay time.Duration, maxRetries int, minRetries int) (*inventoryClient, error) {
	clientConfig := client.Config{}
	var err error

	clientConfig.URL, err = createUrl(inventoryURL)
	if err != nil {
		return nil, err
	}

	var certs *x509.CertPool
	if insecure {
		logger.Warn("Certificate verification is turned off. This is not recommended in production environments")
	} else {
		certs, err = readCACertificate(caPath, logger)
		if err != nil {
			return nil, err
		}
	}

	transport := requestid.Transport(&http.Transport{
		Proxy: proxyFunc,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure,
			RootCAs:            certs,
		},
	})
	// Add retry settings
	tr := rehttp.NewTransport(
		transport,
		rehttp.RetryAny(
			rehttp.RetryAll(
				rehttp.RetryMaxRetries(minRetries),
				rehttp.RetryStatuses(404, 423, 425),
			),
			rehttp.RetryAll(
				rehttp.RetryMaxRetries(maxRetries),
				rehttp.RetryStatuses(408, 429),
			),
			rehttp.RetryAll(
				rehttp.RetryMaxRetries(maxRetries),
				rehttp.RetryStatusInterval(500, 600),
			),
			rehttp.RetryAll(
				rehttp.RetryMaxRetries(maxRetries),
				rehttp.RetryTemporaryErr(),
			),
			rehttp.RetryAll(
				rehttp.RetryMaxRetries(maxRetries),
				RetryConnectionRefusedErr(),
			),
		),
		rehttp.ExpJitterDelay(retryMinDelay, retryMaxDelay),
	)

	clientConfig.Transport = tr

	clientConfig.AuthInfo = auth.AgentAuthHeaderWriter(pullSecret)
	assistedInstallClient := client.New(clientConfig)
	cache := ttlCache.NewCache()
	cache.SetTTL(20 * time.Second)
	cache.SkipTTLExtensionOnHit(true)

	return &inventoryClient{assistedInstallClient, strfmt.UUID(clusterId), logger, cache}, nil
}

func RetryConnectionRefusedErr() rehttp.RetryFn {
	return func(attempt rehttp.Attempt) bool {
		if operr, ok := attempt.Error.(*net.OpError); ok {
			if syserr, ok := operr.Err.(*os.SyscallError); ok {
				if syserr.Err == syscall.ECONNREFUSED {
					return true
				}
			}
		}

		return false
	}
}

func readCACertificate(capath string, logger logrus.FieldLogger) (*x509.CertPool, error) {

	if capath == "" {
		return nil, nil
	}

	caData, err := os.ReadFile(capath)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		return nil, fmt.Errorf("certificate corrupted or in invalid format: %s", capath)
	} else {
		logger.Infof("Using custom CA certificate: %s", capath)
	}

	return pool, nil
}

func (c *inventoryClient) DownloadFile(ctx context.Context, filename string, dest string) error {
	// open output file
	fo, err := os.Create(dest)
	if err != nil {
		return err
	}
	// close fo on exit and check for its returned error
	defer func() {
		fo.Close()
	}()
	c.logger.Infof("Downloading file %s to %s", filename, dest)
	_, err = c.ai.Installer.V2DownloadClusterFiles(ctx, c.createDownloadParams(filename), fo)
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) DownloadClusterCredentials(ctx context.Context, filename string, dest string) error {
	// open output file
	fo, err := os.Create(dest)
	if err != nil {
		return err
	}
	// close fo on exit and check for its returned error
	defer func() {
		fo.Close()
	}()
	c.logger.Infof("Downloading cluster credentials %s to %s", filename, dest)

	params := installer.V2DownloadClusterCredentialsParams{
		ClusterID: c.clusterId,
		FileName:  filename,
	}
	_, err = c.ai.Installer.V2DownloadClusterCredentials(ctx, &params, fo)
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) DownloadHostIgnition(ctx context.Context, infraEnvID string, hostID string, dest string) error {
	// open output file
	fo, err := os.Create(dest)
	if err != nil {
		return err
	}
	// close fo on exit and check for its returned error
	defer func() {
		fo.Close()
	}()

	params := installer.V2DownloadHostIgnitionParams{
		InfraEnvID: strfmt.UUID(infraEnvID),
		HostID:     strfmt.UUID(hostID),
	}
	_, err = c.ai.Installer.V2DownloadHostIgnition(ctx, &params, fo)
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) UpdateHostInstallProgress(ctx context.Context, infraEnvId, hostId string, newStage models.HostStage, info string) error {
	_, err := c.ai.Installer.V2UpdateHostInstallProgress(ctx, c.createUpdateHostInstallProgressParams(infraEnvId, hostId, newStage, info))
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) UploadIngressCa(ctx context.Context, ingressCA string, clusterId string) error {
	_, err := c.ai.Installer.V2UploadClusterIngressCert(ctx,
		&installer.V2UploadClusterIngressCertParams{ClusterID: strfmt.UUID(clusterId), IngressCertParams: models.IngressCertParams(ingressCA)})
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) GetCluster(ctx context.Context, withHosts bool) (*models.Cluster, error) {
	cluster, err := c.ai.Installer.V2GetCluster(ctx, &installer.V2GetClusterParams{ClusterID: c.clusterId, ExcludeHosts: swag.Bool(!withHosts)})
	if err != nil {
		return nil, err
	}

	return cluster.Payload, nil
}

func (c *inventoryClient) ListsHostsForRole(ctx context.Context, role string) (models.HostList, error) {
	ret, err := c.ai.Installer.ListClusterHosts(ctx, &installer.ListClusterHostsParams{ClusterID: c.clusterId, Role: swag.String(role)})
	if err != nil {
		return nil, err
	}
	return ret.Payload, nil
}

func (c *inventoryClient) getMonitoredOperators(ctx context.Context, clusterId string) (models.MonitoredOperatorsList, error) {
	cacheKey := fmt.Sprintf("getMonitoredOperators-%s", clusterId)
	if val, err := c.cache.Get(cacheKey); err == nil {
		return val.(models.MonitoredOperatorsList), nil
	}

	monitoredOperators, err := c.ai.Operators.V2ListOfClusterOperators(ctx, &operators.V2ListOfClusterOperatorsParams{
		ClusterID: strfmt.UUID(clusterId),
	})
	if err != nil {
		return nil, aserror.GetAssistedError(err)
	}
	c.cache.Set(cacheKey, monitoredOperators.Payload)
	return monitoredOperators.Payload, nil
}

func (c *inventoryClient) GetClusterMonitoredOperator(ctx context.Context, clusterId, operatorName string, openshiftVersion string) (*models.MonitoredOperator, error) {
	monitoredOperators, err := c.getMonitoredOperators(ctx, clusterId)
	if err != nil {
		return nil, err
	}
	for _, operator := range monitoredOperators {
		/*
			Check the OCP version is 4.8 and rename the operator name and subscriptionName to ocs,
			This is a temporary fix to a problem which will be removed when we stop supporting OCP4.8 in day1 operation
			This is needed as Assisted Service returns ODF operator in case of all the OCP versions but in case of OCP4.8,
			we deploy OCS, the Installer should check for ocs-operator instead of odf as odf will not be found and eventually
			it will fail.
		*/
		v1, err := version.NewVersion(openshiftVersion)
		if err != nil {
			return nil, err
		}
		constraints, err := version.NewConstraint(">= 4.8, < 4.9")
		if err != nil {
			return nil, err
		}
		if operator.Name == "odf" && constraints.Check(v1) {
			operator.Name = "ocs"
			operator.SubscriptionName = "ocs-operator"
		}
		if operator.Name == operatorName {
			return operator, nil
		}
	}

	return nil, fmt.Errorf("operator %s not found", operatorName)
}

func (c *inventoryClient) GetClusterMonitoredOLMOperators(ctx context.Context, clusterId string, openshiftVersion string) ([]models.MonitoredOperator, error) {
	monitoredOperators, err := c.getMonitoredOperators(ctx, clusterId)
	if err != nil {
		return nil, err
	}

	olmOperators := make([]models.MonitoredOperator, 0)
	for _, operator := range monitoredOperators {
		if operator.OperatorType == models.OperatorTypeOlm {

			/*
				Check the OCP version is 4.8 and rename the operator name and subscriptionName to ocs,
				This is a temporary fix to a problem which will be removed when we stop supporting OCP4.8 in day1 operation
				This is needed as Assisted Service returns ODF operator in case of all the OCP versions but in case of OCP4.8,
				we deploy OCS, the Installer should check for ocs-operator instead of odf as odf will not be found and eventually
				it will fail.
			*/
			v1, err := version.NewVersion(openshiftVersion)
			if err != nil {
				return nil, err
			}
			constraints, err := version.NewConstraint(">= 4.8, < 4.9")
			if err != nil {
				return nil, err
			}
			if operator.Name == "odf" && constraints.Check(v1) {
				operator.Name = "ocs"
				operator.SubscriptionName = "ocs-operator"
			}
			olmOperators = append(olmOperators, *operator)
		}
	}

	return olmOperators, nil
}

func (c *inventoryClient) GetEnabledHostsNamesHosts(ctx context.Context, log logrus.FieldLogger) (map[string]HostData, error) {
	return c.GetHosts(ctx, log, []string{models.HostStatusDisabled})
}

func (c *inventoryClient) GetHosts(ctx context.Context, log logrus.FieldLogger, skippedStatuses []string) (map[string]HostData, error) {
	namesIdsMap := make(map[string]HostData)
	hosts, err := c.getHostsWithInventoryInfo(ctx, log, skippedStatuses)
	if err != nil {
		return nil, err
	}
	for _, hostData := range hosts {
		hostname := strings.ToLower(hostData.Host.RequestedHostname)
		ips, err := utils.GetHostIpsFromInventory(hostData.Inventory)
		if err != nil {
			log.WithError(err).Errorf("failed to get ips of node %s", hostname)
		}
		hostData.IPs = ips
		namesIdsMap[hostname] = hostData
	}
	return namesIdsMap, nil
}

func createUrl(baseURL string) (*url.URL, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, client.DefaultBasePath)
	return u, nil
}

func (c *inventoryClient) createDownloadParams(filename string) *installer.V2DownloadClusterFilesParams {
	return &installer.V2DownloadClusterFilesParams{
		ClusterID: c.clusterId,
		FileName:  filename,
	}
}

func (c *inventoryClient) createUpdateHostInstallProgressParams(infraEnvId, hostId string, newStage models.HostStage, info string) *installer.V2UpdateHostInstallProgressParams {
	return &installer.V2UpdateHostInstallProgressParams{
		InfraEnvID: strfmt.UUID(infraEnvId),
		HostID:     strfmt.UUID(hostId),
		HostProgress: &models.HostProgress{
			CurrentStage: newStage,
			ProgressInfo: info,
		},
	}
}

func (c *inventoryClient) getHostsWithInventoryInfo(ctx context.Context, log logrus.FieldLogger, skippedStatuses []string) (map[string]HostData, error) {
	hostsWithHwInfo := make(map[string]HostData)
	clusterData, err := c.GetCluster(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, host := range clusterData.Hosts {
		if funk.IndexOf(skippedStatuses, *host.Status) > -1 {
			continue
		}
		hwInfo := models.Inventory{}
		err = json.Unmarshal([]byte(host.Inventory), &hwInfo)
		if err != nil {
			log.Warnf("Failed to parse host %s inventory %s", host.ID.String(), host.Inventory)
			return nil, err
		}
		hostsWithHwInfo[host.ID.String()] = HostData{Inventory: &hwInfo, Host: host}
	}
	return hostsWithHwInfo, nil
}

func (c *inventoryClient) CompleteInstallation(ctx context.Context, clusterId string, isSuccess bool, errorInfo string, data map[string]interface{}) error {
	_, err := c.ai.Installer.V2CompleteInstallation(ctx,
		&installer.V2CompleteInstallationParams{ClusterID: strfmt.UUID(clusterId),
			CompletionParams: &models.CompletionParams{IsSuccess: &isSuccess, ErrorInfo: errorInfo, Data: data}})
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) UploadLogs(ctx context.Context, clusterId string, logsType models.LogsType, upfile io.Reader) error {
	fileName := fmt.Sprintf("%s_logs.tar.gz", string(logsType))
	_, err := c.ai.Installer.V2UploadLogs(ctx,
		&installer.V2UploadLogsParams{ClusterID: strfmt.UUID(clusterId), LogsType: string(logsType),
			Upfile: runtime.NamedReader(fileName, upfile)})
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) ClusterLogProgressReport(ctx context.Context, clusterId string, progress models.LogsState) {
	_, err := c.ai.Installer.V2UpdateClusterLogsProgress(ctx, &installer.V2UpdateClusterLogsProgressParams{
		ClusterID: strfmt.UUID(clusterId),
		LogsProgressParams: &models.LogsProgressParams{
			LogsState: &progress,
		},
	})
	if err != nil {
		c.logger.WithError(err).Errorf("failed to report log progress %s on cluster", progress)
	}
}

func (c *inventoryClient) HostLogProgressReport(ctx context.Context, infraEnvId string, hostId string, progress models.LogsState) {
	_, err := c.ai.Installer.V2UpdateHostLogsProgress(ctx, &installer.V2UpdateHostLogsProgressParams{
		InfraEnvID: strfmt.UUID(infraEnvId),
		HostID:     strfmt.UUID(hostId),
		LogsProgressParams: &models.LogsProgressParams{
			LogsState: &progress,
		},
	})
	if err != nil {
		c.logger.WithError(err).Errorf("failed to report log progress %s on host %s", progress, hostId)
	}
}

func (c *inventoryClient) UpdateClusterOperator(ctx context.Context, clusterId string, operatorName string, operatorVersion string, operatorStatus models.OperatorStatus, operatorStatusInfo string) error {
	// Service api expects odf
	if operatorName == "ocs" {
		operatorName = "odf"
	}
	_, err := c.ai.Operators.V2ReportMonitoredOperatorStatus(ctx, &operators.V2ReportMonitoredOperatorStatusParams{
		ClusterID: c.clusterId,
		ReportParams: &models.OperatorMonitorReport{
			Name:       operatorName,
			Status:     operatorStatus,
			StatusInfo: operatorStatusInfo,
			Version:    operatorVersion,
		},
	})
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) TriggerEvent(ctx context.Context, ev *models.Event) error {
	et := strfmt.DateTime(time.Now())
	ev.EventTime = &et
	_, err := c.ai.Events.V2TriggerEvent(ctx, &events.V2TriggerEventParams{
		TriggerEventParams: ev,
	})
	return err
}

func (c *inventoryClient) UpdateFinalizingProgress(ctx context.Context, clusterId string, stage models.FinalizingStage) error {
	params := &installer.V2UpdateClusterFinalizingProgressParams{
		ClusterID: strfmt.UUID(clusterId),
		FinalizingProgress: &models.ClusterFinalizingProgress{
			FinalizingStage: stage,
		},
	}
	_, err := c.ai.Installer.V2UpdateClusterFinalizingProgress(ctx, params)
	return err
}
