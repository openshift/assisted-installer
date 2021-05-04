package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/utils"

	"github.com/kelseyhightower/envconfig"
	assistedinstallercontroller "github.com/openshift/assisted-installer/src/assisted_installer_controller"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-service/pkg/secretdump"
	"github.com/sirupsen/logrus"
)

var Options struct {
	ControllerConfig assistedinstallercontroller.ControllerConfig
}

func main() {
	logger := logrus.New()

	err := envconfig.Process("myapp", &Options)
	if err != nil {
		log.Fatal(err.Error())
	}

	logger.Infof("Start running Assisted-Controller. Configuration is:\n %s", secretdump.DumpSecretStruct(Options.ControllerConfig))

	kc, err := k8s_client.NewK8SClient("", logger)
	if err != nil {
		log.Fatalf("Failed to create k8 client %v", err)
	}

	err = kc.SetProxyEnvVars()
	if err != nil {
		log.Fatalf("Failed to set env vars for installer-controller pod %v", err)
	}

	client, err := inventory_client.CreateInventoryClient(Options.ControllerConfig.ClusterID,
		Options.ControllerConfig.URL, Options.ControllerConfig.PullSecretToken, Options.ControllerConfig.SkipCertVerification,
		Options.ControllerConfig.CACertPath, logger, utils.ProxyFromEnvVars)
	if err != nil {
		log.Fatalf("Failed to create inventory client %v", err)
	}

	assistedController := assistedinstallercontroller.NewController(logger,
		Options.ControllerConfig,
		ops.NewOps(logger, false),
		client,
		kc,
	)

	// While adding new routine don't miss to add wg.add(1)
	// without adding it will panic
	var wg sync.WaitGroup
	status := assistedinstallercontroller.NewControllerStatus()

	ctxApprove, cancelApprove := context.WithCancel(context.Background())
	go assistedController.ApproveCsrs(ctxApprove, &wg)
	wg.Add(1)
	go assistedController.PostInstallConfigs(&wg, status)
	wg.Add(1)
	go assistedController.UpdateBMHs(&wg)
	wg.Add(1)

	ctxInstallationListener, cancelListener := context.WithCancel(context.Background())
	go assistedController.WaitForCancel(ctxInstallationListener, status)

	ctxLogs, cancelLogs := context.WithCancel(context.Background())
	status.AddWatch(cancelLogs)
	go assistedController.UploadLogs(ctxLogs, status)

	assistedController.SetReadyState()
	assistedController.WaitAndUpdateNodesStatus(status)
	logger.Infof("Sleeping for 10 minutes to give a chance to approve all csrs")
	time.Sleep(10 * time.Minute)
	cancelApprove()

	logger.Infof("Waiting for all go routines to finish")
	wg.Wait()
	cancelListener()
	cancelLogs()
}
