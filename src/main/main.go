package main

import (
	"fmt"
	"os"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/installer"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/k8s_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/utils"
)

const (
	Failed = "Failed"
)

func main() {
	config.ProcessArgs()
	logger := utils.InitLogger(config.GlobalConfig.Verbose)
	logger.Infof("Assisted installer started. Configuration is:\n %+v", config.GlobalConfig)
	ai := installer.NewAssistedInstaller(logger,
		config.GlobalConfig,
		ops.NewOps(logger),
		inventory_client.CreateInventoryClient(config.GlobalConfig.ClusterID, config.GlobalConfig.Host, config.GlobalConfig.Port, logger),
		k8s_client.NewK8SClient,
	)
	if err := ai.InstallNode(); err != nil {
		ai.UpdateHostStatus(fmt.Sprintf("%s %s", Failed, err))
		os.Exit(1)
	}
}
