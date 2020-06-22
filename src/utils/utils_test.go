package utils

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/golang/mock/gomock"

	"github.com/sirupsen/logrus/hooks/test"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

func TestUtils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

var _ = Describe("Verify_utils", func() {
	var (
		l = logrus.New()
	)
	l.SetOutput(ioutil.Discard)
	Context("Verify ignition parsing for pull secret", func() {
		It("test GetPullSecretFromIgnition", func() {
			ignitionData, err := ioutil.ReadFile("../../test_files/test_ignition.ign")
			Expect(err).NotTo(HaveOccurred())
			_, err = GetFileContentFromIgnition(ignitionData, "/root/.docker/config.json")
			Expect(err).NotTo(HaveOccurred())

			// No path in ignition, must fail
			_, err = GetFileContentFromIgnition(ignitionData, "/something")
			Expect(err).To(HaveOccurred())

			// Bad ignition data, must fail
			_, err = GetFileContentFromIgnition(ignitionData[:20], "/something")
			Expect(err).To(HaveOccurred())

		})
	})
	Context("Verify mco and rhcos image mapping", func() {
		It("Get image and mco happy flow", func() {
			openshiftVersion := "4.4"
			image, err := GetRhcosImageByOpenshiftVersion(openshiftVersion)
			Expect(err).NotTo(HaveOccurred())
			Expect(image).Should(Equal(getOpenshiftMapping(openshiftVersion)["rhcos"]))

			mco, err := GetMCOByOpenshiftVersion(openshiftVersion)
			Expect(err).NotTo(HaveOccurred())
			Expect(mco).Should(Equal(getOpenshiftMapping(openshiftVersion)["mco"]))

			supported := IsOpenshiftVersionIsSupported(openshiftVersion)
			Expect(supported).Should(Equal(true))
		})
		It("Get image and mco bad flow", func() {
			openshiftVersion := "bad_version"
			image, err := GetRhcosImageByOpenshiftVersion(openshiftVersion)
			Expect(err).To(HaveOccurred())
			Expect(image).Should(Equal(""))

			mco, err := GetMCOByOpenshiftVersion(openshiftVersion)
			Expect(err).To(HaveOccurred())
			Expect(mco).Should(Equal(""))

			supported := IsOpenshiftVersionIsSupported(openshiftVersion)
			Expect(supported).Should(Equal(false))

		})
	})
	Context("Find files", func() {
		It("Read directory and return found files", func() {
			found, err := GetListOfFilesFromFolder("../../test_files", "*.json")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(found)).Should(Equal(1))
			Expect(filepath.Base(found[0])).Should(Equal("node.json"))

			found, err = GetListOfFilesFromFolder("../../test_files", "*.not_exists")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(found)).Should(Equal(0))

			_, err = GetListOfFilesFromFolder("../../test_files_not_exists", "*.json")
			Expect(err).Should(HaveOccurred())
		})
	})

	Context("remove from string list", func() {
		It("Remove element from string list", func() {
			list := []string{"aaa", "bbb"}
			list2 := FindAndRemoveElementFromStringList(list, "aaa")
			Expect(len(list2)).Should(Equal(1))

			list2 = FindAndRemoveElementFromStringList(list, "no-exists")
			Expect(len(list2)).Should(Equal(2))
		})
	})
	Context("test retry", func() {
		It("test once", func() {
			callCount := 0
			err := Retry(3, time.Millisecond, l, func() error {
				callCount++
				return nil
			})
			Expect(err).Should(BeNil())
			Expect(callCount).Should(Equal(1))

		})
		It("test multiple", func() {
			callCount := 0
			err := Retry(3, time.Millisecond, l, func() error {
				callCount++
				if callCount < 2 {
					return fmt.Errorf("Failed")
				}
				return nil
			})
			Expect(err).Should(BeNil())
			Expect(callCount).Should(Equal(2))

		})
		It("test Failure", func() {
			callCount := 0
			tries := 4
			err := Retry(tries, time.Millisecond, l, func() error {
				callCount++
				return fmt.Errorf("failed again")
			})
			Expect(err).Should(Equal(fmt.Errorf("failed after 4 attempts, last error: failed again")))
			Expect(callCount).Should(Equal(tries))

		})
	})
	Context("test coreosInstlalerLogger", func() {
		var (
			cilogger     *CoreosInstallerLogWriter
			hook         *test.Hook
			logger       *logrus.Logger
			mockbmclient *inventory_client.MockInventoryClient
		)
		udpateStatusSuccess := func(statuses []string) {
			for _, status := range statuses {
				mockbmclient.EXPECT().UpdateHostStatus(status, "hostID").Return(nil).Times(1)

			}
		}

		BeforeEach(func() {
			logger, hook = test.NewNullLogger()
			ctrl := gomock.NewController(GinkgoT())
			mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
			cilogger = NewCoreosInstallerLogWriter(logger, mockbmclient, "hostID")
		})
		It("test log with new line", func() {
			_, err := cilogger.Write([]byte("some log with a new line \n"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(1))

		})
		It("test full progress line", func() {
			udpateStatusSuccess([]string{"Writing image to disk - 56%"})
			_, err := cilogger.Write([]byte("> Read disk 473.8 MiB/844.7 MiB (56%)   \r"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(1))
		})
		It("test partial line", func() {
			_, err := cilogger.Write([]byte("844.7 MiB"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(0))

		})
		It("test partial line - should log", func() {
			udpateStatusSuccess([]string{"Writing image to disk - 58%"})
			testLogs := []string{"> Read ",
				"disk",
				" ",
				"473.6 MiB",
				"/",
				"844.7 MiB",
				" (",
				"58",
				"%)   \r",
			}
			for i := range testLogs {
				_, err := cilogger.Write([]byte(testLogs[i]))
				Expect(err).Should(BeNil())
			}
			Expect(len(hook.Entries)).Should(Equal(1))
		})
		It("test multiple lines", func() {
			udpateStatusSuccess([]string{"Writing image to disk - 55%",
				"Writing image to disk - 60%",
				"Writing image to disk - 66%",
			})
			testLogs := []string{"> Read disk 471.2 MiB/844.7 MiB (55%)   \r",
				"> Read ",
				"disk",
				" ",
				"471.6 MiB",
				"/",
				"844.7 MiB",
				" (",
				"55",
				"%)   \r",
				"> Read ",
				"disk",
				" ",
				"472.1 MiB",
				"/",
				"844.7 MiB",
				" (",
				"55",
				"%)   \r",
				"> Read disk 472.6 MiB/844.7 MiB (55%)   \r",
				"> Read disk 472.8 MiB/844.7 MiB (55%)   \r",
				"> Read disk 472.9 MiB/844.7 MiB (55%)   \r",
				"> Read disk 473.0 MiB/844.7 MiB (60%)   \r",
				"> Read disk 473.3 MiB/844.7 MiB (62%)   \r",
				"> Read ",
				"disk",
				" ",
				"473.6 MiB",
				"/",
				"844.7 MiB",
				" (",
				"56",
				"%)   \r",
				"> Read disk 473.8 MiB/844.7 MiB (66%)   \r"}
			for i := range testLogs {
				_, err := cilogger.Write([]byte(testLogs[i]))
				Expect(err).Should(BeNil())
			}
			Expect(len(hook.Entries)).Should(Equal(10))

		})
	})
})
