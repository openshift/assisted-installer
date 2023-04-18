package common

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
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
	l.SetOutput(io.Discard)
	Context("Verify SetConfiguringStatusForHosts", func() {

		It("test SetConfiguringStatusForHosts", func() {
			var logs string
			logsInBytes, _ := os.ReadFile("../../test_files/mcs_logs.txt")
			logs = string(logsInBytes)
			infraEnvId := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f250")
			node0Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
			node1Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")
			node3Id := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f250")
			testInventoryIdsIps := map[string]inventory_client.HostData{
				"node0": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node0Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster},
					IPs: []string{"192.168.126.10", "192.168.11.122", "fe80::5054:ff:fe9a:4738"}},
				"node1": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster},
					IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting},
					Role: models.HostRoleMaster}, IPs: []string{"192.168.126.13", "192.168.11.125", "2620:52:0:1351:67c2:adb3:cfd6:83"}},
				"node3": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node3Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting},
					Role: models.HostRoleWorker}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}
			// note that in the MCS log we use node 1 IPv6 address
			// note that in the MCS log we use node 2 IPv6 address without the interface name
			// note that node0 IP address (192.168.126.10) is a substring of 192.168.126.100 in the MCS log
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node1Id.String(), models.HostStageConfiguring, gomock.Any()).Return(fmt.Errorf("dummy")).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node2Id.String(), models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node3Id.String(), models.HostStageWaitingForIgnition, gomock.Any()).Return(nil).Times(1)
			SetConfiguringStatusForHosts(mockbmclient, testInventoryIdsIps, logs, true, l)
			Expect(testInventoryIdsIps["node0"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
			Expect(testInventoryIdsIps["node1"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
			Expect(testInventoryIdsIps["node2"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
			Expect(testInventoryIdsIps["node3"].Host.Progress.CurrentStage).Should(Equal(models.HostStageWaitingForIgnition))

			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node1Id.String(), models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), infraEnvId.String(), node3Id.String(), models.HostStageConfiguring, gomock.Any()).Return(nil).Times(1)
			SetConfiguringStatusForHosts(mockbmclient, testInventoryIdsIps, logs, false, l)
			Expect(testInventoryIdsIps["node0"].Host.Progress.CurrentStage).Should(Equal(models.HostStageRebooting))
			Expect(testInventoryIdsIps["node1"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
			Expect(testInventoryIdsIps["node2"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
			Expect(testInventoryIdsIps["node3"].Host.Progress.CurrentStage).Should(Equal(models.HostStageConfiguring))
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

	Context("Verify name- and IP-based matching", func() {
		var testInventoryIdsIps, knownIpAddresses map[string]inventory_client.HostData
		var node0Id, node1Id, node2Id strfmt.UUID

		BeforeEach(func() {
			infraEnvId := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f250")
			node0Id = strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
			node1Id = strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f239")
			node2Id = strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f240")

			testInventoryIdsIps = map[string]inventory_client.HostData{"node0": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node0Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster},
				IPs: []string{"192.168.126.10", "192.168.39.248", "fe80::5054:ff:fe9a:4738"}},
				"node1": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node1Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleMaster}, IPs: []string{"192.168.126.11", "192.168.11.123", "fe80::5054:ff:fe9a:4739"}},
				"node2": {Host: &models.Host{InfraEnvID: infraEnvId, ID: &node2Id, Progress: &models.HostProgressInfo{CurrentStage: models.HostStageRebooting}, Role: models.HostRoleWorker}, IPs: []string{"192.168.126.12", "192.168.11.124", "fe80::5054:ff:fe9a:4740"}}}
			knownIpAddresses = BuildHostsMapIPAddressBased(testInventoryIdsIps)
		})

		It("test BuildHostsMapIPAddressBased", func() {
			Expect(len(knownIpAddresses)).To(Equal(9))
			Expect(knownIpAddresses["192.168.126.10"].Host.ID).To(Equal(&node0Id))
			Expect(knownIpAddresses["192.168.11.123"].Host.ID).To(Equal(&node1Id))
			Expect(knownIpAddresses["fe80::5054:ff:fe9a:4740"].Host.ID).To(Equal(&node2Id))
			Expect(knownIpAddresses["10.0.0.1"]).To(Equal(inventory_client.HostData{IPs: nil, Inventory: nil, Host: nil}))
		})

		It("test HostMatchByNameOrIPAddress by name", func() {
			nodes := GetKubeNodes(map[string]string{"node1": "6d6f00e8-dead-beef-cafe-0f1459485ad9"})
			Expect(len(nodes.Items)).To(Equal(1))
			Expect(nodes.Items[0].Name).To(Equal("node1"))
			match, ok := HostMatchByNameOrIPAddress(nodes.Items[0], testInventoryIdsIps, knownIpAddresses)
			Expect(ok).To(Equal(true))
			Expect(match.Host.ID).To(Equal(&node1Id))
		})

		It("test HostMatchByNameOrIPAddress by IP", func() {
			nodes := GetKubeNodes(map[string]string{"some-fake-name": "6d6f00e8-dead-beef-cafe-0f1459485ad9"})
			Expect(len(nodes.Items)).To(Equal(1))
			Expect(nodes.Items[0].Name).To(Equal("some-fake-name"))
			match, ok := HostMatchByNameOrIPAddress(nodes.Items[0], testInventoryIdsIps, knownIpAddresses)
			Expect(ok).To(Equal(true))
			Expect(match.Host.ID).To(Equal(&node0Id))
		})
	})

	Context("Verify RemoveUninitializedTaint", func() {
		It("nil platform struct should not remove uninitiazed taint", func() {
			removeUninitializedTaint := RemoveUninitializedTaint(nil)
			Expect(removeUninitializedTaint).To(BeFalse())
		})

		for _, test := range []struct {
			PlatformType                     models.PlatformType
			ExpectedRemoveUninitializedTaint bool
		}{
			{
				PlatformType:                     models.PlatformTypeNutanix,
				ExpectedRemoveUninitializedTaint: true,
			},
			{
				PlatformType:                     models.PlatformTypeVsphere,
				ExpectedRemoveUninitializedTaint: true,
			},
			{
				PlatformType:                     models.PlatformTypeNone,
				ExpectedRemoveUninitializedTaint: false,
			},
			{
				PlatformType:                     models.PlatformTypeBaremetal,
				ExpectedRemoveUninitializedTaint: false,
			},
		} {
			platformType := test.PlatformType
			expectedRemoveUninitializedTaint := test.ExpectedRemoveUninitializedTaint

			Context("by platform", func() {
				var platform *models.Platform
				BeforeEach(func() {
					platform = &models.Platform{
						Type: &platformType,
					}
				})

				It(fmt.Sprintf("%v is expected to remove unitialized taint = %v", platformType, expectedRemoveUninitializedTaint), func() {
					removeUninitializedTaint := RemoveUninitializedTaint(platform)
					Expect(removeUninitializedTaint).To(Equal(expectedRemoveUninitializedTaint))
				})
			})
		}
	})
})

func GetKubeNodes(kubeNamesIds map[string]string) *v1.NodeList {
	file, _ := os.ReadFile("../../test_files/node.json")
	var node v1.Node
	_ = json.Unmarshal(file, &node)
	nodeList := &v1.NodeList{}
	for name, id := range kubeNamesIds {
		node.Status.NodeInfo.SystemUUID = id
		node.Name = name
		nodeList.Items = append(nodeList.Items, node)
	}
	return nodeList
}
