package main

import (
	"os"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/installer"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/utils"
)

func main() {
	config.ProcessArgs()
	logger := utils.InitLogger(config.GlobalConfig.Verbose)
	logger.Infof("Assisted installer started. Configuration is:\n %+v", config.GlobalConfig)
	installer := installer.NewAssistedInstaller(logger,
		config.GlobalConfig,
		ops.NewOps(logger),
		inventory_client.CreateBmInventoryClient(),
	)
	if err := installer.InstallNode(); err != nil {
		os.Exit(1)
	}
}
