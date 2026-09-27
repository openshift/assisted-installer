package main

import (
	"os"

	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/installer"
	"github.com/openshift/assisted-installer/src/utils"
)

func main() {
	installerConfig := &config.Config{}
	installerConfig.ProcessArgs(os.Args[1:])
	logger := utils.InitLogger(installerConfig.Verbose, true, installerConfig.ForcedHostID, config.DefaultDryRunConfig.DryRunEnabled)
	installerConfig.PullSecretToken = os.Getenv("PULL_SECRET_TOKEN")
	if installerConfig.PullSecretToken == "" {
		logger.Warnf("Agent Authentication Token not set")
	}
	if err := installer.RunInstaller(installerConfig, logger); err != nil {
		os.Exit(1)
	}
}
