package inventory_client

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
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
		hook      *test.Hook
		client    *inventoryClient
		server    *ghttp.Server
	)

	AfterEach(func() {
		server.Close()
	})

	BeforeEach(func() {
		var err error
		hook = test.NewLocal(logger)
		server = ghttp.NewServer()
		server.SetAllowUnhandledRequests(true)
		server.SetUnhandledRequestStatusCode(http.StatusInternalServerError) // 500

		client, err = CreateInventoryClientWithDelay(clusterID, "http://"+server.Addr(), "pullSecret", true, "",
			logger, nil, testRetryDelay, testRetryMaxDelay, testMaxRetries)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(client).ShouldNot(BeNil())
	})

	Context("UpdateHostInstallProgress", func() {
		var (
			hostID = "host-id"
		)

		It("positive_response", func() {
			expectServerCall(server, fmt.Sprintf("/api/assisted-install/v1/clusters/%s/hosts/%s/progress", clusterID, hostID), 200)
			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).ShouldNot(HaveOccurred())
			checkLogForRetries(hook.Entries[:], 0)
		})

		It("negative_response", func() {
			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).Should(HaveOccurred())
			checkLogForRetries(hook.Entries[:], testMaxRetries)
		})

		It("positive_late_response", func() {
			amountOfRetries := 2

			go func() {
				// Unhandled requests receives 500 as described in BeforeEach section

				time.Sleep(testRetryMaxDelay * time.Duration(amountOfRetries))
				expectServerCall(server, fmt.Sprintf("/api/assisted-install/v1/clusters/%s/hosts/%s/progress", clusterID, hostID), 200)
			}()

			Expect(client.UpdateHostInstallProgress(hostID, models.HostStageInstalling, "")).ShouldNot(HaveOccurred())
			checkLogForRetries(hook.Entries[:], amountOfRetries)
		})
	})
})

func expectServerCall(server *ghttp.Server, path string, statusCode int) {
	var body = []byte("empty")
	server.AppendHandlers(
		ghttp.CombineHandlers(
			ghttp.VerifyRequest("PUT", path),
			ghttp.RespondWithPtr(&statusCode, &body),
		),
	)
}

func checkLogForRetries(entries []logrus.Entry, amount int) {
	counter := 0

	for _, entry := range entries {
		if strings.Contains(entry.Message, "Going to retry") {
			counter++
		}
	}

	Expect(counter).Should(Equal(amount))
}
