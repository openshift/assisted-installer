package main

import (
	"net/http"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/openshift/assisted-installer/src/ignition"
	"github.com/openshift/assisted-installer/src/main/drymock"

	"github.com/golang/mock/gomock"
	"github.com/onsi/ginkgo"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/installer"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	"github.com/openshift/assisted-service/pkg/secretdump"
)

// In dry run mode we prefer to get quick feedback about errors rather
// than keep retrying many times.
const dryRunMaximumInventoryClientRetries = 3

func main() {
	config.ProcessArgs()
	config.ProcessDryRunArgs()
	logger := utils.InitLogger(config.GlobalConfig.Verbose, true, config.GlobalDryRunConfig.ForcedHostID, config.DefaultDryRunConfig.DryRunEnabled)
	config.GlobalConfig.PullSecretToken = os.Getenv("PULL_SECRET_TOKEN")
	if config.GlobalConfig.PullSecretToken == "" {
		logger.Warnf("Agent Authentication Token not set")
	}

	logger.Infof("Assisted installer started. Configuration is:\n %s", secretdump.DumpSecretStruct(config.GlobalConfig))
	logger.Infof("Dry configuration is:\n %s", secretdump.DumpSecretStruct(config.GlobalDryRunConfig))

	numRetries := inventory_client.DefaultMaxRetries
	if config.GlobalDryRunConfig.DryRunEnabled {
		numRetries = dryRunMaximumInventoryClientRetries
	}

	client, err := inventory_client.CreateInventoryClientWithDelay(
		config.GlobalConfig.ClusterID,
		config.GlobalConfig.URL,
		config.GlobalConfig.PullSecretToken,
		config.GlobalConfig.SkipCertVerification,
		config.GlobalConfig.CACertPath,
		logger,
		http.ProxyFromEnvironment,
		inventory_client.DefaultRetryMinDelay,
		inventory_client.DefaultRetryMaxDelay,
		numRetries,
		inventory_client.DefaultMinRetries,
	)

	if err != nil {
		logger.Fatalf("Failed to create inventory client %e", err)
	}

	o := ops.NewOps(logger, true)

	var k8sClientBuilder k8s_client.K8SClientBuilder
	if !config.GlobalDryRunConfig.DryRunEnabled {
		k8sClientBuilder = k8s_client.NewK8SClient
	} else {
		k8sClientBuilder = func(configPath string, logger *logrus.Logger) (k8s_client.K8SClient, error) {
			var kc k8s_client.K8SClient
			mockController := gomock.NewController(ginkgo.GinkgoT())
			kc = k8s_client.NewMockK8SClient(mockController)
			mock, _ := kc.(*k8s_client.MockK8SClient)
			drymock.PrepareInstallerDryK8sMock(mock, logger, o, config.GlobalDryRunConfig.ParsedClusterHosts)
			return kc, nil
		}
	}

	ai := installer.NewAssistedInstaller(logger,
		config.GlobalConfig,
		o,
		client,
		k8sClientBuilder,
		ignition.NewIgnition(),
	)

	// Try to format requested disks. May fail formatting some disks, this is not an error.
	ai.FormatDisks()

	if err := ai.InstallNode(); err != nil {
		ai.UpdateHostInstallProgress(models.HostStageFailed, err.Error())
		os.Exit(1)
	}
}
