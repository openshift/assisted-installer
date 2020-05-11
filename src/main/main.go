package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eranco74/assisted-installer/src/config"
	"github.com/eranco74/assisted-installer/src/installer"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/k8s_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/eranco74/assisted-installer/src/utils"
	"github.com/sirupsen/logrus"
)

const (
	Failed = "Failed"
	Done   = "Done"
)

func main() {
	config.ProcessArgs()
	logger := utils.InitLogger(config.GlobalConfig.Verbose)
	logger.Infof("Assisted installer started. Configuration is:\n %+v", config.GlobalConfig)
	inventoryClient := inventory_client.CreateInventoryClient()
	kubeClient := getKubeClient(logger, inventoryClient)
	ai := installer.NewAssistedInstaller(logger,
		config.GlobalConfig,
		ops.NewOps(logger),
		inventoryClient,
		kubeClient,
	)
	if err := ai.InstallNode(); err != nil {
		ai.UpdateHostStatus(fmt.Sprintf("%s %s", Failed, err))
		os.Exit(1)
	}
	ai.UpdateHostStatus(Done)
}

func getKubeClient(logger *logrus.Logger, ic inventory_client.InventoryClient) k8s_client.K8SClient {
	var kc k8s_client.K8SClient
	// in case of bootstap download the kubeconfig and create the k8s client
	if config.GlobalConfig.Role == installer.HostRoleBootstrap {
		kubeconfigPath, err := getKubeconfig(logger, ic)
		if err != nil {
			logrus.Fatal(err)
		}
		kc, err = k8s_client.NewK8SClient(kubeconfigPath, logger)
		if err != nil {
			logrus.Fatal(err)
		}
	}
	return kc
}

func getKubeconfig(logger *logrus.Logger, ic inventory_client.InventoryClient) (string, error) {
	o := ops.NewOps(logger)
	if err := o.Mkdir(installer.InstallDir); err != nil {
		logger.Errorf("Failed to create install dir: %s", err)
		return "", err
	}
	dest := filepath.Join(installer.InstallDir, installer.Kubeconfig)
	err := ic.DownloadFile(installer.Kubeconfig, dest)
	if err != nil {
		logger.Errorf("Failed to fetch file (%s) from server. err: %s", installer.Kubeconfig, err)
		return "", err
	}
	return dest, nil
}
