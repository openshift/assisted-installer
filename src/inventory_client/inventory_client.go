package inventory_client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/filanov/bm-inventory/models"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/filanov/bm-inventory/client"
	"github.com/filanov/bm-inventory/client/installer"
	"github.com/filanov/bm-inventory/pkg/requestid"
	"github.com/go-openapi/strfmt"
)

//go:generate mockgen -source=inventory_client.go -package=inventory_client -destination=mock_inventory_client.go
type InventoryClient interface {
	DownloadFile(filename string, dest string) error
	UpdateHostStatus(newStatus string) error
}

type inventoryClient struct {
	ai *client.AssistedInstall
}

func CreateInventoryClient() *inventoryClient {
	clientConfig := client.Config{}
	clientConfig.URL, _ = url.Parse(createUrl())
	clientConfig.Transport = requestid.Transport(http.DefaultTransport)
	assistedInstallClient := client.New(clientConfig)
	return &inventoryClient{assistedInstallClient}
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
	_, err = c.ai.Installer.DownloadClusterFiles(context.Background(), createDownloadParams(filename), fo)
	return err
}

func (c *inventoryClient) UpdateHostStatus(newStatus string) error {
	_, err := c.ai.Installer.UpdateHostInstallProgress(context.Background(), createUpdateHostStatusParams(newStatus))
	return err
}

func createUrl() string {
	return fmt.Sprintf("http://%s:%d/%s",
		config.GlobalConfig.Host,
		config.GlobalConfig.Port,
		client.DefaultBasePath,
	)
}

func createDownloadParams(filename string) *installer.DownloadClusterFilesParams {
	return &installer.DownloadClusterFilesParams{
		ClusterID: strfmt.UUID(config.GlobalConfig.ClusterID),
		FileName:  filename,
	}
}

func createUpdateHostStatusParams(newStatus string) *installer.UpdateHostInstallProgressParams {
	return &installer.UpdateHostInstallProgressParams{
		ClusterID:                 strfmt.UUID(config.GlobalConfig.ClusterID),
		HostID:                    strfmt.UUID(config.GlobalConfig.HostID),
		HostInstallProgressParams: models.HostInstallProgressParams(newStatus),
	}
}
