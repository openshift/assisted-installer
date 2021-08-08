package common

import (
	"fmt"
	"io/ioutil"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/inventory_client"
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
			infraEnvId := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f250")
			node0Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
			node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")
			testInventoryIdsIps := map[string]inventory_client.HostData{"node0": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node0Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster},
				IPs: []string{"192.168.126.10", "192.168.11.122", "fe80::5054:ff:fe9a:4738"}},
				"node1": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster}, IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleWorker}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}

			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node1Id.String(), models.HostStageConfiguring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node2Id.String(), models.HostStageWaitingForIgnition, gomock.Any()).Return(nil).Times(1)
			SetConfiguringStatusForHosts(mockbmclient, testInventoryIdsIps, logs, true, l)
			Expect(testInventoryIdsIps["node0"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
			Expect(testInventoryIdsIps["node1"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
			Expect(testInventoryIdsIps["node2"].Host.Progress.CurrentStage).Should(Equal(models.HostStageWaitingForIgnition))

			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node1Id.String(), models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node2Id.String(), models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			SetConfiguringStatusForHosts(mockbmclient, testInventoryIdsIps, logs, false, l)
			Expect(testInventoryIdsIps["node1"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
			Expect(testInventoryIdsIps["node2"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
			Expect(testInventoryIdsIps["node0"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
		})
	})

	Context("GetHostsInStatus", func() {
		var (
			testID     = strfmt.UUID(uuid.New().String())
			testStatus = models.HostStatusError
		)

		tests := []struct {
			name          string
			isMatch       bool
			originalHosts map[string]inventory_client.HostData
			status        []string
			expectedHosts map[string]inventory_client.HostData
		}{
			{
				name:    "ask for match - match found -> keep",
				isMatch: true,
				originalHosts: map[string]inventory_client.HostData{
					"node0": {Host: &models.Host{ID: &testID, Status: &testStatus}},
				},
				status: []string{testStatus},
				expectedHosts: map[string]inventory_client.HostData{
					"node0": {Host: &models.Host{ID: &testID, Status: &testStatus}},
				},
			},
			{
				name:    "ask for match - match not found -> remove",
				isMatch: true,
				originalHosts: map[string]inventory_client.HostData{
					"node0": {Host: &models.Host{ID: &testID, Status: &testStatus}},
				},
				status:        []string{models.HostStatusInstalled},
				expectedHosts: map[string]inventory_client.HostData{},
			},
			{
				name:    "ask for no match - match found -> remove",
				isMatch: false,
				originalHosts: map[string]inventory_client.HostData{
					"node0": {Host: &models.Host{ID: &testID, Status: &testStatus}},
				},
				status:        []string{testStatus},
				expectedHosts: map[string]inventory_client.HostData{},
			},
			{
				name:    "ask for no match - match not found -> keep",
				isMatch: false,
				originalHosts: map[string]inventory_client.HostData{
					"node0": {Host: &models.Host{ID: &testID, Status: &testStatus}},
				},
				status: []string{models.HostStatusInstalled},
				expectedHosts: map[string]inventory_client.HostData{
					"node0": {Host: &models.Host{ID: &testID, Status: &testStatus}},
				},
			},
		}

		for i := range tests {
			test := tests[i]
			It(test.name, func() {
				res := GetHostsInStatus(test.originalHosts, test.status, test.isMatch)
				Expect(test.expectedHosts).To(Equal(res))
			})
		}
	})
})
