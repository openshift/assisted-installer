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

	status := assistedinstallercontroller.NewControllerStatus()

	// While adding new routine don't miss to add wg.add(1)
	// without adding it will panic
	var wg sync.WaitGroup
	var wgLogs sync.WaitGroup

	//every context should be added to the watch list before calling
	//the routines because they may be canceled within those routines
	//espceially, they should be called before WaitAndUpdateNodesStatus()
	//that listen to the user's abort command

	ctxApprove, cancelApprove := context.WithTimeout(context.Background(), 10*time.Minute)
	status.AddWatch(cancelApprove)
	bmhconfig, bmhcancel := context.WithCancel(context.Background())
	status.AddWatch(bmhcancel)
	ctxLogs, cancelLogs := context.WithCancel(context.Background())
	status.AddWatch(cancelLogs)
	ctxConfig, cancelConfig := context.WithCancel(context.Background())
	status.AddWatch(cancelConfig)

	go assistedController.ApproveCsrs(ctxApprove, &wg)
	wg.Add(1)

	go assistedController.UpdateBMHs(bmhconfig, &wg)
	wg.Add(1)

	go assistedController.UploadLogs(ctxLogs, &wgLogs, status)
	wgLogs.Add(1)

	assistedController.SetReadyState()
	assistedController.WaitAndUpdateNodesStatus(status)

	go assistedController.PostInstallConfigs(ctxConfig, &wg, status)
	wg.Add(1)

	logger.Infof("Waiting for all go routines to finish")
	wg.Wait()
	// if !status.HasError() {
	// 	//with error the logs are canceled within UploadLogs
	// 	logger.Infof("closing logs...")
	// 	cancelLogs()
	// }
	wgLogs.Wait()
}
