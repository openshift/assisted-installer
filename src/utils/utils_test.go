package utils

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

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
})
