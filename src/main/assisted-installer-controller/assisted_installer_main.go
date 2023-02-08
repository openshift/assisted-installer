package main

import (
	"context"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/openshift/assisted-installer/src/common"

	"github.com/openshift/assisted-installer/src/ops/execute"

	"github.com/golang/mock/gomock"
	"github.com/kelseyhightower/envconfig"
	assistedinstallercontroller "github.com/openshift/assisted-installer/src/assisted_installer_controller"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/main/drymock"
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

const (
	maximumInventoryClientRetries = 15
	maximumErrorsBeforeExit       = 3
	bootstrapKubeconfigForSNO     = "/tmp/bootstrap-secrets/kubeconfig"
)

func DryRebootComplete() bool {
	if _, err := os.Stat(Options.ControllerConfig.DryFakeRebootMarkerPath); err == nil {
		return true
	}

	return false
}

func main() {
	logger := logrus.New()
	logger.SetReportCaller(true)
	err := envconfig.Process("myapp", &Options)
	if err != nil {
		log.Fatal(err.Error())
	}

	if Options.ControllerConfig.DryRunEnabled {
		if err = config.DryParseClusterHosts(Options.ControllerConfig.DryRunClusterHostsPath, &Options.ControllerConfig.ParsedClusterHosts); err != nil {
			log.Fatalf("Failed to parse dry cluster hosts: %v", err)
		}

		// In dry run mode, we need to wait for the reboot to complete before starting the controller
		for !DryRebootComplete() {
			time.Sleep(time.Second * 1)
		}
	}

	logFile, err := os.OpenFile(common.ControllerLogFile, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	if err != nil {
		log.Fatal(err.Error())
	}
	mw := io.MultiWriter(os.Stdout, logFile)
	logger.SetOutput(mw)

	logger.Infof("Start running Assisted-Controller. Configuration is:\n %s", secretdump.DumpSecretStruct(Options.ControllerConfig))

	executor := execute.NewExecutor(&config.Config{}, logger, false)
	o := ops.NewOps(logger, executor)

	var kc k8s_client.K8SClient
	if !Options.ControllerConfig.DryRunEnabled {
		// in case of sno we want controller to start as fast as possible,
		//in that case we want to use kubeconfig from filesystem and not pulling it from api
		k8sConfigPath := ""
		if _, errB := os.Stat(bootstrapKubeconfigForSNO); errB == nil {
			k8sConfigPath = bootstrapKubeconfigForSNO
		}
		kc, err = k8s_client.NewK8SClient(k8sConfigPath, logger)
		if err != nil {
			log.Fatalf("Failed to create k8 client %v", err)
		}
	} else {
		mockController := gomock.NewController(logger)
		kc = k8s_client.NewMockK8SClient(mockController)
		mock, _ := kc.(*k8s_client.MockK8SClient)
		drymock.PrepareControllerDryMock(mock, logger, o, Options.ControllerConfig.ParsedClusterHosts)
	}

	err = kc.SetProxyEnvVars()
	if err != nil {
		log.Fatalf("Failed to set env vars for installer-controller pod %v", err)
	}

	// everything in assisted-controller runs in loops, we prefer to fail early on error and to retry on the next loop
	// this will allow us to show service error more quickly
	// Currently we will retry maximum for 10 times per call
	client, err := inventory_client.CreateInventoryClientWithDelay(Options.ControllerConfig.ClusterID,
		Options.ControllerConfig.URL, Options.ControllerConfig.PullSecretToken, Options.ControllerConfig.SkipCertVerification,
		Options.ControllerConfig.CACertPath, logger, utils.ProxyFromEnvVars, inventory_client.DefaultRetryMinDelay,
		inventory_client.DefaultRetryMaxDelay, maximumInventoryClientRetries, inventory_client.DefaultMinRetries)
	if err != nil {
		log.Fatalf("Failed to create inventory client %v", err)
	}

	assistedController := assistedinstallercontroller.NewController(logger,
		Options.ControllerConfig,
		o,
		client,
		kc,
	)

	var wg sync.WaitGroup
	mainContext, mainContextCancel := context.WithCancel(context.Background())

	// No need to cancel with context, will finish quickly
	// we should fix try to fix dns service issue as soon as possible
	if !Options.ControllerConfig.DryRunEnabled {
		// This check is unnecessary in dry run mode, and mocking for it is complicated
		go assistedController.HackDNSAddressConflict(&wg)
		wg.Add(1)
	}
	cluster := assistedController.SetReadyState()

	// While adding new routine don't miss to add wg.add(1)
	// without adding it will panic

	defer func() {
		// stop all go routines
		mainContextCancel()
		logger.Infof("Waiting for all go routines to finish")
		wg.Wait()
		logger.Infof("Finished all")
	}()

	removeUninitializedTaint := false
	if cluster.Platform != nil && *cluster.Platform.Type == models.PlatformTypeVsphere {
		removeUninitializedTaint = true
	}
	go assistedController.WaitAndUpdateNodesStatus(mainContext, &wg, removeUninitializedTaint)
	wg.Add(1)
	go assistedController.PostInstallConfigs(mainContext, &wg)
	wg.Add(1)

	if *cluster.Platform.Type == models.PlatformTypeBaremetal {
		go assistedController.UpdateBMHs(mainContext, &wg)
		wg.Add(1)
	} else {
		logger.Infof("Cluster platform is not %s, skipping BMH", models.PlatformTypeBaremetal)
	}

	go assistedController.UploadLogs(mainContext, &wg)
	wg.Add(1)

	go assistedController.UpdateNodeLabels(mainContext, &wg)
	wg.Add(1)

	// monitoring installation by cluster status
	waitForInstallation(client, logger, assistedController.Status)
}

// waitForInstallation monitor cluster status and is blocking main from cancelling all go routine s
// if cluster status is (cancelled,installed) there is no need to continue and we will exit.
// if cluster is in error, in addition to stop waiting we need to set error status to tell upload logs to send must-gather.
// if we have maximumErrorsBeforeExit GetClusterNotFound/GetClusterUnauthorized errors in a row we force exiting controller
func waitForInstallation(client inventory_client.InventoryClient, log logrus.FieldLogger, status *assistedinstallercontroller.ControllerStatus) {
	log.Infof("monitor cluster installation status")
	reqCtx := utils.GenerateRequestContext()
	errCounter := 0

	for {
		time.Sleep(waitForInstallationInterval)
		cluster, err := client.GetCluster(reqCtx, false)
		if err != nil {
			// In case cluster was deleted or controller is not authorised
			// we should exit controller after maximumErrorsBeforeExit errors
			// in case cluster was deleted we should exit immediately
			switch err.(type) {
			case *installer.V2GetClusterNotFound:
				errCounter = errCounter + maximumErrorsBeforeExit
				log.WithError(err).Errorf("Cluster was not found in inventory or user is not authorized")
			case *installer.V2GetClusterUnauthorized:
				errCounter++
				log.WithError(err).Errorf("User is not authenticated to perform the operation")
			}

			// if we get maximumErrorsBeforeExit errors in a row
			// there is no point to try to reach assisted service
			// we should exit with 0 cause in case of another exit status
			// job will restart assisted-controller.
			if errCounter >= maximumErrorsBeforeExit {
				log.Infof("Got more than %d errors from assisted service in a row, exiting", maximumErrorsBeforeExit)
				exit(0)
			}
			continue
		}
		// reset error counter in case no error occurred
		errCounter = 0
		finished := didInstallationFinish(*cluster.Status, log, status)
		if finished {
			return
		}
	}
}

func didInstallationFinish(clusterStatus string, log logrus.FieldLogger, status *assistedinstallercontroller.ControllerStatus) bool {
	switch clusterStatus {
	case models.ClusterStatusError:
		log.Infof("Cluster installation failed.")
		status.Error()
		return true
	case models.ClusterStatusCancelled:
		log.Infof("Cluster installation aborted. Signal the status")
		return true
	case models.ClusterStatusInstalled:
		log.Infof("Cluster installation successfully finished.")
		return true
	case models.ClusterStatusAddingHosts:
		log.Infof("Cluster is day2")
		return true
	}
	return false
}
