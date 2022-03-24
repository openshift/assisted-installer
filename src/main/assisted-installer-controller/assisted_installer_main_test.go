package main

import (
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	"github.com/go-openapi/swag"
	"github.com/openshift/assisted-service/client/installer"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	assistedinstallercontroller "github.com/openshift/assisted-installer/src/assisted_installer_controller"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "controller_main_test")
}

var _ = Describe("installer HostRoleMaster role", func() {
	var (
		l            = logrus.New()
		ctrl         *gomock.Controller
		mockbmclient *inventory_client.MockInventoryClient
		status       *assistedinstallercontroller.ControllerStatus
	)

	l.SetOutput(ioutil.Discard)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		waitForInstallationInterval = 10 * time.Millisecond
		status = assistedinstallercontroller.NewControllerStatus()
	})
	AfterEach(func() {
		ctrl.Finish()
	})

	It("Waiting for cluster installed - first cluster error then installed", func() {
		// fail to connect to assisted and then succeed
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, fmt.Errorf("dummy")).Times(1)
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)},
			nil).Times(1)
		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(false))
	})

	It("Waiting for cluster cancelled", func() {
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusCancelled)},
			nil).Times(1)
		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(false))
	})

	It("Waiting for cluster error - should set error status", func() {
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusError)},
			nil).Times(1)
		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(true))
	})

	It("Waiting for cluster Unauthorized - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterUnauthorized()).Times(maximumErrorsBeforeExit)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)
		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Not found - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterNotFound()).Times(maximumErrorsBeforeExit)

		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Not found  - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterNotFound()).Times(1)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Unauthorized  - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterUnauthorized()).Times(maximumErrorsBeforeExit)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Unauthorized less then needed for exit and then installed", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterUnauthorized()).Times(maximumErrorsBeforeExit - 2)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, status)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(1))
	})

})
