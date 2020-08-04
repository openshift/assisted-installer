package main

import (
	"os"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/installer"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/k8s_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
)

func main() {
	config.ProcessArgs()
	logger := utils.InitLogger(config.GlobalConfig.Verbose, true)
	logger.Infof("Assisted installer started. Configuration is:\n %+v", config.GlobalConfig)
	client, err := inventory_client.CreateInventoryClient(config.GlobalConfig.ClusterID, config.GlobalConfig.URL, logger)
	if err != nil {
		logger.Fatalf("Failed to create inventory client %e", err)
	}

	ai := installer.NewAssistedInstaller(logger,
		config.GlobalConfig,
		ops.NewOps(logger),
		client,
		k8s_client.NewK8SClient,
	)
	if err := ai.InstallNode(); err != nil {
		ai.UpdateHostInstallProgress(models.HostStageFailed, err.Error())
		os.Exit(1)
	}
}
