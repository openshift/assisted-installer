package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	assistedinstallercontroller "github.com/openshift/assisted-installer/src/assisted_installer_controller"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/client/installer"
	"github.com/openshift/assisted-service/models"
	"github.com/openshift/assisted-service/pkg/secretdump"
	"github.com/sirupsen/logrus"
)

// Added this way to be able to test it
var (
	exit                        = os.Exit
	waitForInstallationInterval = 1 * time.Minute
)

var Options struct {
	ControllerConfig assistedinstallercontroller.ControllerConfig
}

const maximumErrorsBeforeExit = 10

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

	assistedController.SetReadyState()

	// While adding new routine don't miss to add wg.add(1)
	// without adding it will panic
	var wg sync.WaitGroup
	var status assistedinstallercontroller.ControllerStatus

	ctxRoutines, cancelRoutines := context.WithCancel(context.Background())
	go assistedController.WaitAndUpdateNodesStatus(ctxRoutines, &wg)
	wg.Add(1)
	go assistedController.PostInstallConfigs(ctxRoutines, &wg, &status)
	wg.Add(1)
	go assistedController.UpdateBMHs(ctxRoutines, &wg)
	wg.Add(1)

	// No need to cancel with context, will finish quickly
	go assistedController.HackDNSAddressConflict(&wg)
	wg.Add(1)

	go assistedController.UploadLogs(ctxRoutines, &wg, &status)
	wg.Add(1)

	// monitoring cluster status
	// after it will finish we will stop all go routines
	waitForInstallation(client, logger, &status)
	// stop all go routines
	cancelRoutines()

	logger.Infof("Waiting for all go routines to finish")
	wg.Wait()
	logger.Infof("Finished all")
}

// waitForInstallation monitor cluster status and is blocking main from cancelling all go routine s
// if cluster cancelled,installed there is no need to continue and we will exit.
// if cluster is in error, in addition to stop waiting we need to set error status to tell upload logs to send must-gather.
// if we have maximumErrorsBeforeExit GetClusterNotFound/GetClusterUnauthorized errors in a row we force exiting controller
func waitForInstallation(client inventory_client.InventoryClient, log logrus.FieldLogger, status *assistedinstallercontroller.ControllerStatus) {
	log.Infof("monitor cluster installation status")
	reqCtx := utils.GenerateRequestContext()
	errCounter := 0

	for {
		time.Sleep(waitForInstallationInterval)
		cluster, err := client.GetCluster(reqCtx)
		if err != nil {
			// In case cluster was deleted or controller is not authorised
			// we should exit controller after maximumErrorsBeforeExit errors
			switch err.(type) {
			case *installer.GetClusterNotFound:
				errCounter++
				log.WithError(err).Errorf("Cluster was not found in inventory or user is not authorized")
			case *installer.GetClusterUnauthorized:
				errCounter++
				log.WithError(err).Errorf("User is not authenticated to perform the operation")
			}

			// if we get maximumErrorsBeforeExit errors in a row
			// there is no point to try to reach assisted service
			// exit all
			if errCounter >= maximumErrorsBeforeExit {
				log.Infof("Got more than %d errors from assisted service in a row, exiting", maximumErrorsBeforeExit)
				exit(0)
			}
			continue
		}
		// zero error counter in case no error occured
		errCounter = 0
		switch *cluster.Status {
		case models.ClusterStatusError:
			log.Infof("Cluster installation failed.")
			status.Error()
			return
		case models.ClusterStatusCancelled:
			log.Infof("Cluster installation aborted. Signal the status")
			return
		case models.ClusterStatusInstalled:
			log.Infof("Cluster installation successfully finished.")
			return
		}
	}
}
