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
	"github.com/openshift/assisted-service/models"
	"github.com/openshift/assisted-service/pkg/auth"
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
	DownloadFile(filename string, dest string) error
	UpdateHostInstallProgress(hostId string, newStage models.HostStage, info string) error
	GetEnabledHostsNamesHosts() (map[string]HostData, error)
	UploadIngressCa(ingressCA string, clusterId string) error
	GetCluster() (*models.Cluster, error)
	CompleteInstallation(clusterId string, isSuccess bool, errorInfo string) error
	GetHosts(skippedStatuses []string) (map[string]HostData, error)
	UploadLogs(clusterId string, logsType models.LogsType, upfile io.Reader) error
}

type inventoryClient struct {
	log       *logrus.Logger
	ai        *client.AssistedInstall
	clusterId strfmt.UUID
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
	return &inventoryClient{logger, assistedInstallClient, strfmt.UUID(clusterId)}, nil
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

func (c *inventoryClient) DownloadFile(filename string, dest string) error {
	// open output file
	fo, err := os.Create(dest)
	if err != nil {
		return err
	}
	// close fo on exit and check for its returned error
	defer func() {
		fo.Close()
	}()
	_, err = c.ai.Installer.DownloadClusterFiles(context.Background(), c.createDownloadParams(filename), fo)
	return err
}

func (c *inventoryClient) UpdateHostInstallProgress(hostId string, newStage models.HostStage, info string) error {
	_, err := c.ai.Installer.UpdateHostInstallProgress(context.Background(), c.createUpdateHostInstallProgressParams(hostId, newStage, info))
	return err
}

func (c *inventoryClient) UploadIngressCa(ingressCA string, clusterId string) error {
	_, err := c.ai.Installer.UploadClusterIngressCert(context.Background(),
		&installer.UploadClusterIngressCertParams{ClusterID: strfmt.UUID(clusterId), IngressCertParams: models.IngressCertParams(ingressCA)})
	return err
}

func (c *inventoryClient) GetCluster() (*models.Cluster, error) {
	cluster, err := c.ai.Installer.GetCluster(context.Background(), &installer.GetClusterParams{ClusterID: c.clusterId})
	if err != nil {
		return nil, err
	}

	return cluster.Payload, nil
}

func (c *inventoryClient) GetEnabledHostsNamesHosts() (map[string]HostData, error) {
	return c.GetHosts([]string{models.HostStatusDisabled})
}

func (c *inventoryClient) GetHosts(skippedStatuses []string) (map[string]HostData, error) {
	namesIdsMap := make(map[string]HostData)
	hosts, err := c.getHostsWithInventoryInfo(skippedStatuses)
	if err != nil {
		return nil, err
	}
	for _, hostData := range hosts {
		hostname := strings.ToLower(hostData.Host.RequestedHostname)
		ips, err := utils.GetHostIpsFromInventory(hostData.Inventory)
		if err != nil {
			c.log.WithError(err).Errorf("failed to get ips of node %s", hostname)
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

func (c *inventoryClient) getHostsWithInventoryInfo(skippedStatuses []string) (map[string]HostData, error) {
	hostsWithHwInfo := make(map[string]HostData)
	hosts, err := c.ai.Installer.ListHosts(context.Background(), &installer.ListHostsParams{ClusterID: c.clusterId})
	if err != nil {
		return nil, err
	}
	for _, host := range hosts.Payload {
		if funk.IndexOf(skippedStatuses, *host.Status) > -1 {
			continue
		}
		hwInfo := models.Inventory{}
		err = json.Unmarshal([]byte(host.Inventory), &hwInfo)
		if err != nil {
			c.log.Warnf("Failed to parse host %s inventory %s", host.ID.String(), host.Inventory)
			return nil, err
		}
		hostsWithHwInfo[host.ID.String()] = HostData{Inventory: &hwInfo, Host: host}
	}
	return hostsWithHwInfo, nil
}

func (c *inventoryClient) CompleteInstallation(clusterId string, isSuccess bool, errorInfo string) error {
	_, err := c.ai.Installer.CompleteInstallation(context.Background(),
		&installer.CompleteInstallationParams{ClusterID: strfmt.UUID(clusterId),
			CompletionParams: &models.CompletionParams{IsSuccess: &isSuccess, ErrorInfo: errorInfo}})
	return err
}

func (c *inventoryClient) UploadLogs(clusterId string, logsType models.LogsType, upfile io.Reader) error {
	fileName := fmt.Sprintf("%s_logs.tar.gz", string(logsType))
	_, err := c.ai.Installer.UploadLogs(context.Background(),
		&installer.UploadLogsParams{ClusterID: strfmt.UUID(clusterId), LogsType: string(logsType),
			Upfile: runtime.NamedReader(fileName, upfile)})
	return err
}
