package inventory_client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/go-openapi/runtime"

	"github.com/thoas/go-funk"

	"github.com/PuerkitoBio/rehttp"
	"github.com/go-openapi/strfmt"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/client"
	"github.com/openshift/assisted-service/client/installer"
	"github.com/openshift/assisted-service/client/operators"
	"github.com/openshift/assisted-service/models"
	"github.com/openshift/assisted-service/pkg/auth"
	aserror "github.com/openshift/assisted-service/pkg/error"
	"github.com/openshift/assisted-service/pkg/requestid"
	"github.com/sirupsen/logrus"
)

const (
	defaultRetryMinDelay = time.Duration(2) * time.Second
	defaultRetryMaxDelay = time.Duration(10) * time.Second
	defaultMaxRetries    = 10
)

//go:generate mockgen -source=inventory_client.go -package=inventory_client -destination=mock_inventory_client.go
type InventoryClient interface {
	DownloadFile(ctx context.Context, filename string, dest string) error
	DownloadHostIgnition(ctx context.Context, hostID string, dest string) error
	UpdateHostInstallProgress(ctx context.Context, hostId string, newStage models.HostStage, info string) error
	GetEnabledHostsNamesHosts(ctx context.Context, log logrus.FieldLogger) (map[string]HostData, error)
	UploadIngressCa(ctx context.Context, ingressCA string, clusterId string) error
	GetCluster(ctx context.Context) (*models.Cluster, error)
	GetClusterMonitoredOperator(ctx context.Context, clusterId, operatorName string) (*models.MonitoredOperator, error)
	GetClusterMonitoredOLMOperators(ctx context.Context, clusterId string) ([]models.MonitoredOperator, error)
	CompleteInstallation(ctx context.Context, clusterId string, isSuccess bool, errorInfo string) error
	GetHosts(ctx context.Context, log logrus.FieldLogger, skippedStatuses []string) (map[string]HostData, error)
	UploadLogs(ctx context.Context, clusterId string, logsType models.LogsType, upfile io.Reader) error
	ClusterLogProgressReport(ctx context.Context, clusterId string, progress models.LogsState)
	HostLogProgressReport(ctx context.Context, clusterId string, hostId string, progress models.LogsState)
	UpdateClusterOperator(ctx context.Context, clusterId string, operatorName string, operatorStatus models.OperatorStatus, operatorStatusInfo string) error
}

type inventoryClient struct {
	ai        *client.AssistedInstall
	clusterId strfmt.UUID
	logger    *logrus.Logger
}

type HostData struct {
	IPs       []string
	Inventory *models.Inventory
	Host      *models.Host
}

func CreateInventoryClient(clusterId string, inventoryURL string, pullSecret string, insecure bool, caPath string,
	logger *logrus.Logger, proxyFunc func(*http.Request) (*url.URL, error)) (*inventoryClient, error) {
	return CreateInventoryClientWithDelay(clusterId, inventoryURL, pullSecret, insecure, caPath,
		logger, proxyFunc, defaultRetryMinDelay, defaultRetryMaxDelay, defaultMaxRetries)
}

func CreateInventoryClientWithDelay(clusterId string, inventoryURL string, pullSecret string, insecure bool, caPath string,
	logger *logrus.Logger, proxyFunc func(*http.Request) (*url.URL, error),
	retryMinDelay, retryMaxDelay time.Duration, maxRetries int) (*inventoryClient, error) {
	clientConfig := client.Config{}
	var err error
	clientConfig.URL, err = url.ParseRequestURI(createUrl(inventoryURL))
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
				rehttp.RetryMaxRetries(maxRetries),
				rehttp.RetryStatusInterval(400, 600),
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
	return &inventoryClient{assistedInstallClient, strfmt.UUID(clusterId), logger}, nil
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

func readCACertificate(capath string, logger *logrus.Logger) (*x509.CertPool, error) {

	if capath == "" {
		return nil, nil
	}

	caData, err := ioutil.ReadFile(capath)
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
	_, err = c.ai.Installer.DownloadClusterFiles(ctx, c.createDownloadParams(filename), fo)
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) DownloadHostIgnition(ctx context.Context, hostID string, dest string) error {
	// open output file
	fo, err := os.Create(dest)
	if err != nil {
		return err
	}
	// close fo on exit and check for its returned error
	defer func() {
		fo.Close()
	}()

	params := installer.DownloadHostIgnitionParams{
		ClusterID: c.clusterId,
		HostID:    strfmt.UUID(hostID),
	}
	_, err = c.ai.Installer.DownloadHostIgnition(ctx, &params, fo)
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) UpdateHostInstallProgress(ctx context.Context, hostId string, newStage models.HostStage, info string) error {
	_, err := c.ai.Installer.UpdateHostInstallProgress(ctx, c.createUpdateHostInstallProgressParams(hostId, newStage, info))
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) UploadIngressCa(ctx context.Context, ingressCA string, clusterId string) error {
	_, err := c.ai.Installer.UploadClusterIngressCert(ctx,
		&installer.UploadClusterIngressCertParams{ClusterID: strfmt.UUID(clusterId), IngressCertParams: models.IngressCertParams(ingressCA)})
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) GetCluster(ctx context.Context) (*models.Cluster, error) {
	cluster, err := c.ai.Installer.GetCluster(ctx, &installer.GetClusterParams{ClusterID: c.clusterId})
	if err != nil {
		return nil, aserror.GetAssistedError(err)
	}

	return cluster.Payload, nil
}

func (c *inventoryClient) GetClusterMonitoredOperator(ctx context.Context, clusterId, operatorName string) (*models.MonitoredOperator, error) {
	monitoredOperators, err := c.ai.Operators.ListOfClusterOperators(ctx, &operators.ListOfClusterOperatorsParams{
		ClusterID:    strfmt.UUID(clusterId),
		OperatorName: &operatorName,
	})
	if err != nil {
		return nil, aserror.GetAssistedError(err)
	}

	return monitoredOperators.Payload[0], nil
}

func (c *inventoryClient) GetClusterMonitoredOLMOperators(ctx context.Context, clusterId string) ([]models.MonitoredOperator, error) {
	monitoredOperators, err := c.ai.Operators.ListOfClusterOperators(ctx, &operators.ListOfClusterOperatorsParams{ClusterID: strfmt.UUID(clusterId)})
	if err != nil {
		return nil, aserror.GetAssistedError(err)
	}

	olmOperators := make([]models.MonitoredOperator, 0)
	for _, operator := range monitoredOperators.Payload {
		if operator.OperatorType == models.OperatorTypeOlm {
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

func createUrl(baseURL string) string {
	return fmt.Sprintf("%s/%s",
		baseURL,
		client.DefaultBasePath,
	)
}

func (c *inventoryClient) createDownloadParams(filename string) *installer.DownloadClusterFilesParams {
	return &installer.DownloadClusterFilesParams{
		ClusterID: c.clusterId,
		FileName:  filename,
	}
}

func (c *inventoryClient) createUpdateHostInstallProgressParams(hostId string, newStage models.HostStage, info string) *installer.UpdateHostInstallProgressParams {
	return &installer.UpdateHostInstallProgressParams{
		ClusterID: c.clusterId,
		HostID:    strfmt.UUID(hostId),
		HostProgress: &models.HostProgress{
			CurrentStage: newStage,
			ProgressInfo: info,
		},
	}
}

func (c *inventoryClient) getHostsWithInventoryInfo(ctx context.Context, log logrus.FieldLogger, skippedStatuses []string) (map[string]HostData, error) {
	hostsWithHwInfo := make(map[string]HostData)
	hosts, err := c.ai.Installer.ListHosts(ctx, &installer.ListHostsParams{ClusterID: c.clusterId})
	if err != nil {
		return nil, aserror.GetAssistedError(err)
	}
	for _, host := range hosts.Payload {
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

func (c *inventoryClient) CompleteInstallation(ctx context.Context, clusterId string, isSuccess bool, errorInfo string) error {
	_, err := c.ai.Installer.CompleteInstallation(ctx,
		&installer.CompleteInstallationParams{ClusterID: strfmt.UUID(clusterId),
			CompletionParams: &models.CompletionParams{IsSuccess: &isSuccess, ErrorInfo: errorInfo}})
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) UploadLogs(ctx context.Context, clusterId string, logsType models.LogsType, upfile io.Reader) error {
	fileName := fmt.Sprintf("%s_logs.tar.gz", string(logsType))
	_, err := c.ai.Installer.UploadLogs(ctx,
		&installer.UploadLogsParams{ClusterID: strfmt.UUID(clusterId), LogsType: string(logsType),
			Upfile: runtime.NamedReader(fileName, upfile)})
	return aserror.GetAssistedError(err)
}

func (c *inventoryClient) ClusterLogProgressReport(ctx context.Context, clusterId string, progress models.LogsState) {
	_, err := c.ai.Installer.UpdateClusterLogsProgress(ctx, &installer.UpdateClusterLogsProgressParams{
		ClusterID: strfmt.UUID(clusterId),
		LogsProgressParams: &models.LogsProgressParams{
			LogsState: progress,
		},
	})
	if err != nil {
		c.logger.WithError(err).Errorf("failed to report log progress %s on cluster", progress)
	}
}

func (c *inventoryClient) HostLogProgressReport(ctx context.Context, clusterId string, hostId string, progress models.LogsState) {
	_, err := c.ai.Installer.UpdateHostLogsProgress(ctx, &installer.UpdateHostLogsProgressParams{
		ClusterID: strfmt.UUID(clusterId),
		HostID:    strfmt.UUID(hostId),
		LogsProgressParams: &models.LogsProgressParams{
			LogsState: progress,
		},
	})
	if err != nil {
		c.logger.WithError(err).Errorf("failed to report log progress %s on host %s", progress, hostId)
	}
}

func (c *inventoryClient) UpdateClusterOperator(ctx context.Context, clusterId string, operatorName string, operatorStatus models.OperatorStatus, operatorStatusInfo string) error {
	_, err := c.ai.Operators.ReportMonitoredOperatorStatus(ctx, &operators.ReportMonitoredOperatorStatusParams{
		ClusterID: c.clusterId,
		ReportParams: &models.OperatorMonitorReport{
			Name:       operatorName,
			Status:     operatorStatus,
			StatusInfo: operatorStatusInfo,
		},
	})
	return aserror.GetAssistedError(err)
}
