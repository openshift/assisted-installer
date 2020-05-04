package main

import (
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

func main() {
	config.ProcessArgs()
	logger := utils.InitLogger(config.GlobalConfig.Verbose)
	logger.Infof("Assisted installer started. Configuration is:\n %+v", config.GlobalConfig)

	installer := installer.NewAssistedInstaller(logger,
		config.GlobalConfig,
		ops.NewOps(logger),
		inventory_client.CreateBmInventoryClient(),
		getKubeClient(logger),
	)
	if err := installer.InstallNode(); err != nil {
		os.Exit(1)
	}
}

func getKubeClient(logger *logrus.Logger) k8s_client.K8SClient {
	var kc k8s_client.K8SClient
	// in case of bootstap download the kubeconfig and create the k8s client
	if config.GlobalConfig.Role == installer.HostRoleBootstrap {
		kubeconfigPath, err := getKubeconfig(logger)
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

func getKubeconfig(logger *logrus.Logger) (string, error) {
	o := ops.NewOps(logger)
	ic := inventory_client.CreateBmInventoryClient()
	if err := o.Mkdir(installer.InstallDir); err != nil {
		logger.Errorf("Failed to create install dir: %s", err)
		return "", err
	}
	filename := "kubeconfig"
	dest := filepath.Join(installer.InstallDir, filename)
	err := ic.DownloadFile(filename, dest)
	if err != nil {
		logger.Errorf("Failed to fetch file (%s) from server. err: %s", filename, err)
		return "", err
	}
	return dest, nil
}
