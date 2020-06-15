package main

import (
	"log"
	"sync"
	"time"

	"github.com/eranco74/assisted-installer/src/k8s_client"

	assistedinstallercontroller "github.com/eranco74/assisted-installer/src/assisted_installer_controller"
	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/eranco74/assisted-installer/src/ops"
	"github.com/kelseyhightower/envconfig"
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

	kc, err := k8s_client.NewK8SClient("", logger)
	if err != nil {
		log.Fatalf("Failed to create k8 client %e", err)
	}

	logger.Infof("Start running assisted-installer with cluster-id %s, host %s , port %d ",
		Options.ControllerConfig.ClusterID, Options.ControllerConfig.Host, Options.ControllerConfig.Port)

	assistedController := assistedinstallercontroller.NewController(logger,
		Options.ControllerConfig,
		ops.NewOps(logger),
		inventory_client.CreateInventoryClient(Options.ControllerConfig.ClusterID, Options.ControllerConfig.Host, Options.ControllerConfig.Port, logger),
		kc,
	)
	var wg sync.WaitGroup
	done := make(chan bool)
	wg.Add(2)
	go assistedController.ApproveCsrs(done, &wg)
	go assistedController.AddRouterCAToClusterCA(&wg)
	assistedController.WaitAndUpdateNodesStatus()
	logger.Infof("Sleeping for 10 minutes to give a chance to approve all crs")
	time.Sleep(10 * time.Minute)
	done <- true
	logger.Infof("Waiting fo all go routines to finish")
	wg.Wait()
}
