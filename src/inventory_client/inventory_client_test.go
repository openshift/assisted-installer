package inventory_client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "inventory_test")
}

const (
	testRetryDelay    = time.Duration(1) * time.Second
	testRetryMaxDelay = time.Duration(1) * time.Second
	testMaxRetries    = 4
)

var _ = Describe("inventory_client_tests", func() {
	var (
		clusterID = "cluster-id"
		logger    = logrus.New()
		client    *inventoryClient
		server    *ghttp.Server
	)

	AfterEach(func() {
		server.Close()
	})

	BeforeEach(func() {
		var err error
		server = ghttp.NewUnstartedServer()
		server.SetAllowUnhandledRequests(true)
		server.SetUnhandledRequestStatusCode(http.StatusInternalServerError) // 500
		client, err = CreateInventoryClientWithDelay(clusterID, "http://"+server.Addr(), "pullSecret", true, "",
			logger, nil, testRetryDelay, testRetryMaxDelay, testMaxRetries)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(client).ShouldNot(BeNil())
	})

	Context("UpdateHostInstallProgress", func() {
		var (
			hostID       = "host-id"
			expectedJson = make(map[string]string)
		)

		BeforeEach(func() {
			expectedJson["current_stage"] = "Installing"
		})

		It("positive_response", func() {
			server.Start()
			expectServerCall(server, fmt.Sprintf("/api/assisted-install/v1/clusters/%s/hosts/%s/progress", clusterID, hostID), expectedJson, http.StatusOK)
			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).ShouldNot(HaveOccurred())
			Expect(server.ReceivedRequests()).Should(HaveLen(1))

		})

		It("negative_server_error_response", func() {
			server.Start()
			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).Should(HaveOccurred())
			Expect(server.ReceivedRequests()).Should(HaveLen(testMaxRetries + 1))

		})

		It("positive_late_response", func() {
			server.Start()
			expectServerCall(server, fmt.Sprintf("/api/assisted-install/v1/clusters/%s/hosts/%s/progress", clusterID, hostID), expectedJson, http.StatusInternalServerError)
			expectServerCall(server, fmt.Sprintf("/api/assisted-install/v1/clusters/%s/hosts/%s/progress", clusterID, hostID), expectedJson, http.StatusForbidden)
			expectServerCall(server, fmt.Sprintf("/api/assisted-install/v1/clusters/%s/hosts/%s/progress", clusterID, hostID), expectedJson, http.StatusOK)

			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).ShouldNot(HaveOccurred())
			Expect(server.ReceivedRequests()).Should(HaveLen(3))
		})

		It("server_partially available", func() {
			go func() {

				time.Sleep(testRetryMaxDelay * 2)
				expectServerCall(server, fmt.Sprintf("/api/assisted-install/v1/clusters/%s/hosts/%s/progress", clusterID, hostID), expectedJson, http.StatusOK)
				server.Start()
			}()

			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).ShouldNot(HaveOccurred())
			Expect(server.ReceivedRequests()).Should(HaveLen(1))
		})

		It("server_down", func() {
			server.Start()
			server.Close()
			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).Should(HaveOccurred())
		})
	})
})

func expectServerCall(server *ghttp.Server, path string, expectedJson interface{}, returnedStatusCode int) {
	var body = []byte("empty")
	data, err := json.Marshal(expectedJson)
	Expect(err).ShouldNot(HaveOccurred())
	content := string(data) + "\n"

	server.AppendHandlers(
		ghttp.CombineHandlers(
			ghttp.VerifyRequest("PUT", path),
			ghttp.VerifyJSON(string(data)),
			ghttp.VerifyHeader(http.Header{"Content-Length": []string{strconv.Itoa(len(content))}}),
			ghttp.RespondWithPtr(&returnedStatusCode, &body),
		),
	)
}
