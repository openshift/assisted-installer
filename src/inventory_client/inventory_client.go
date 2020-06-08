package inventory_client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/filanov/bm-inventory/models"

	"github.com/filanov/bm-inventory/client"
	"github.com/filanov/bm-inventory/client/installer"
	"github.com/filanov/bm-inventory/pkg/requestid"
	"github.com/go-openapi/strfmt"
)

//go:generate mockgen -source=inventory_client.go -package=inventory_client -destination=mock_inventory_client.go
type InventoryClient interface {
	DownloadFile(filename string, dest string) error
	UpdateHostStatus(newStatus string, hostId string) error
	GetHostsIds() ([]string, error)
	UploadIngressCa(ingressCA string, clusterId string) error
	GetCluster() (*models.Cluster, error)
}

type inventoryClient struct {
	ai        *client.AssistedInstall
	clusterId strfmt.UUID
}

func CreateInventoryClient(clusterId string, host string, port int) *inventoryClient {
	clientConfig := client.Config{}
	clientConfig.URL, _ = url.Parse(createUrl(host, port))
	clientConfig.Transport = requestid.Transport(http.DefaultTransport)
	assistedInstallClient := client.New(clientConfig)
	return &inventoryClient{assistedInstallClient, strfmt.UUID(clusterId)}
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

func (c *inventoryClient) GetHostsIds() ([]string, error) {
	var hostIds []string
	hosts, err := c.ai.Installer.ListHosts(context.Background(), &installer.ListHostsParams{ClusterID: c.clusterId})
	if err != nil {
		return nil, err
	}
	for _, host := range hosts.Payload {
		hostIds = append(hostIds, host.ID.String())
	}
	return hostIds, nil
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
