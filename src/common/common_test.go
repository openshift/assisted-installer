package common

import (
	"fmt"
	"io/ioutil"
	"testing"

	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/go-openapi/strfmt"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
)

func TestCommon(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

var _ = Describe("verify common", func() {
	var (
		l            = logrus.New()
		mockbmclient *inventory_client.MockInventoryClient
	)
	mockbmclient = inventory_client.NewMockInventoryClient(gomock.NewController(GinkgoT()))
	l.SetOutput(ioutil.Discard)
	Context("Verify SetConfiguringStatusForHosts", func() {

		It("test SetConfiguringStatusForHosts", func() {
			var logs string
			logsInBytes, _ := ioutil.ReadFile("../../test_files/mcs_logs.txt")
			logs = string(logsInBytes)
			node0Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
			node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")
			testInventoryIdsIps := map[string]inventory_client.EnabledHostData{"node0": {Host: &models.Host{ID: &node0Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster},
				IPs: []string{"192.168.126.10", "192.168.11.122", "fe80::5054:ff:fe9a:4738"}},
				"node1": {Host: &models.Host{ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster}, IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleWorker}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}

			mockbmclient.EXPECT().UpdateHostInstallProgress(node1Id.String(), models.HostStageConfiguring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(node2Id.String(), models.HostStageWaitingForIgnition, gomock.Any()).Return(nil).Times(1)
			SetConfiguringStatusForHosts(mockbmclient, testInventoryIdsIps, logs, true, l)
			Expect(testInventoryIdsIps["node0"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
			Expect(testInventoryIdsIps["node1"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
			Expect(testInventoryIdsIps["node2"].Host.Progress.CurrentStage).Should(Equal(models.HostStageWaitingForIgnition))

			mockbmclient.EXPECT().UpdateHostInstallProgress(node1Id.String(), models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(node2Id.String(), models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			SetConfiguringStatusForHosts(mockbmclient, testInventoryIdsIps, logs, false, l)
			Expect(testInventoryIdsIps["node1"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
			Expect(testInventoryIdsIps["node2"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
			Expect(testInventoryIdsIps["node0"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
		})
	})

})
