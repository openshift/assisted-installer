package inventory_client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/filanov/bm-inventory/models"

	"github.com/filanov/bm-inventory/client"
	"github.com/filanov/bm-inventory/client/installer"
	"github.com/filanov/bm-inventory/pkg/requestid"
	"github.com/go-openapi/strfmt"
	"github.com/sirupsen/logrus"
)

//go:generate mockgen -source=inventory_client.go -package=inventory_client -destination=mock_inventory_client.go
type InventoryClient interface {
	DownloadFile(filename string, dest string) error
	UpdateHostStatus(newStatus string, hostId string) error
	GetEnabledHostsNamesIds() (map[string]string, error)
	UploadIngressCa(ingressCA string, clusterId string) error
	GetCluster() (*models.Cluster, error)
	GetEnabledIdsIps() (map[string][]string, error)
}

type inventoryClient struct {
	log       *logrus.Logger
	ai        *client.AssistedInstall
	clusterId strfmt.UUID
}

type enabledHostData struct {
	inventory *models.Inventory
	host      *models.Host
}

func CreateInventoryClient(clusterId string, host string, port int, logger *logrus.Logger) *inventoryClient {
	clientConfig := client.Config{}
	clientConfig.URL, _ = url.Parse(createUrl(host, port))
	clientConfig.Transport = requestid.Transport(http.DefaultTransport)
	assistedInstallClient := client.New(clientConfig)
	return &inventoryClient{logger, assistedInstallClient, strfmt.UUID(clusterId)}
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

func (c *inventoryClient) UpdateHostStatus(newStatus string, hostId string) error {
	_, err := c.ai.Installer.UpdateHostInstallProgress(context.Background(), c.createUpdateHostStatusParams(newStatus, hostId))
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

func (c *inventoryClient) GetEnabledHostsNamesIds() (map[string]string, error) {
	namesIdsMap := make(map[string]string)
	hosts, err := c.getEnabledHostsWithInventoryInfo()
	if err != nil {
		return nil, err
	}
	for hostId, hostData := range hosts {
		hostname := hostData.host.RequestedHostname
		if hostname == "" {
			hostname = hostData.inventory.Hostname
		}
		namesIdsMap[hostname] = hostId
	}
	return namesIdsMap, nil
}

func (c *inventoryClient) GetEnabledIdsIps() (map[string][]string, error) {
	idIpsMap := make(map[string][]string)
	hosts, err := c.getEnabledHostsWithInventoryInfo()
	if err != nil {
		return nil, err
	}
	for hostId, hostData := range hosts {
		for _, netInt := range hostData.inventory.Interfaces {
			for _, ip := range append(netInt.IPV4Addresses, netInt.IPV6Addresses...) {
				parsedIp, _, err := net.ParseCIDR(ip)
				if err != nil {
					c.log.Warnf("Failed to parse ip %s for host %s", ip, hostId)
					continue
				}
				idIpsMap[hostId] = append(idIpsMap[hostId], parsedIp.String())
			}
		}
	}
	return idIpsMap, nil
}

func createUrl(host string, port int) string {
	return fmt.Sprintf("http://%s:%d/%s",
		host,
		port,
		client.DefaultBasePath,
	)
}

func (c *inventoryClient) createDownloadParams(filename string) *installer.DownloadClusterFilesParams {
	return &installer.DownloadClusterFilesParams{
		ClusterID: c.clusterId,
		FileName:  filename,
	}
}

func (c *inventoryClient) createUpdateHostStatusParams(newStatus string, hostId string) *installer.UpdateHostInstallProgressParams {
	return &installer.UpdateHostInstallProgressParams{
		ClusterID:                 c.clusterId,
		HostID:                    strfmt.UUID(hostId),
		HostInstallProgressParams: models.HostInstallProgressParams(newStatus),
	}
}

func (c *inventoryClient) getEnabledHostsWithInventoryInfo() (map[string]enabledHostData, error) {
	hostsWithHwInfo := make(map[string]enabledHostData)
	hosts, err := c.ai.Installer.ListHosts(context.Background(), &installer.ListHostsParams{ClusterID: c.clusterId})
	if err != nil {
		return nil, err
	}
	for _, host := range hosts.Payload {
		if *host.Status == models.HostStatusDisabled {
			continue
		}
		hwInfo := models.Inventory{}
		err = json.Unmarshal([]byte(host.Inventory), &hwInfo)
		if err != nil {
			c.log.Warnf("Failed to parse host %s inventory %s", host.ID.String(), host.Inventory)
			return nil, err
		}
		hostsWithHwInfo[host.ID.String()] = enabledHostData{inventory: &hwInfo, host: host}
	}
	return hostsWithHwInfo, nil
}
