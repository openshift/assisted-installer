package main

import (
	"log"
	"sync"
	"time"

	"github.com/openshift/assisted-installer/src/k8s_client"

	"github.com/kelseyhightower/envconfig"
	assistedinstallercontroller "github.com/openshift/assisted-installer/src/assisted_installer_controller"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/ops"
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
		log.Fatalf("Failed to create k8 client %v", err)
	}

	logger.Infof("Start running assisted-installer with cluster-id %s, url %s",
		Options.ControllerConfig.ClusterID, Options.ControllerConfig.URL)

	err = kc.SetProxyEnvVars()
	if err != nil {
		log.Fatalf("Failed to set env vars for installer-controller pod %v", err)
	}

	client, err := inventory_client.CreateInventoryClient(Options.ControllerConfig.ClusterID, Options.ControllerConfig.URL, Options.ControllerConfig.PullSecretToken, Options.ControllerConfig.SkipCertVerification, Options.ControllerConfig.CACertPath, logger)
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
	done := make(chan bool)
	go assistedController.ApproveCsrs(done, &wg)
	wg.Add(1)
	go assistedController.PostInstallConfigs(&wg)
	wg.Add(1)
	go assistedController.UpdateBMHs(&wg)
	wg.Add(1)

	assistedController.WaitAndUpdateNodesStatus()
	logger.Infof("Sleeping for 10 minutes to give a chance to approve all crs")
	time.Sleep(10 * time.Minute)
	done <- true
	logger.Infof("Waiting fo all go routines to finish")
	wg.Wait()
}
