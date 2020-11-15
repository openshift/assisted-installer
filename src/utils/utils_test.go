package utils

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-openapi/swag"
	"github.com/openshift/assisted-service/client/installer"
	"github.com/openshift/assisted-service/models"

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

var _ = Describe("Error Utils", func() {

	It("AssistedServiceErrorAPI tests", func() {

		err := installer.DownloadHostIgnitionConflict{
			Payload: &models.Error{
				Href:   swag.String("href"),
				ID:     swag.Int32(555),
				Kind:   swag.String("kind"),
				Reason: swag.String("reason"),
			},
		}

		expectedFmt := "AssistedServiceError Code:  Href: href ID: 555 Kind: kind Reason: reason"

		By("test AssistedServiceError original error - expect bad error formatting", func() {
			Expect(err.Error()).ShouldNot(Equal(expectedFmt))
		})

		By("test AssistedServiceError error - expect good formatting", func() {
			ase := GetAssistedError(&err)
			Expect(ase).Should(HaveOccurred())
			Expect(ase.Error()).Should(Equal(expectedFmt))
		})
	})

	It("AssistedServiceInfraError tests", func() {

		err := installer.DownloadHostIgnitionForbidden{
			Payload: &models.InfraError{
				Code:    swag.Int32(403),
				Message: swag.String("forbidden"),
			},
		}

		expectedFmt := "AssistedServiceInfraError Code: 403 Message: forbidden"

		By("test AssistedServiceInfraError original error - expect bad error formatting", func() {
			Expect(err.Error()).ShouldNot(Equal(expectedFmt))
		})

		By("test AssistedServiceInfraError error - expect good formatting", func() {
			ase := GetAssistedError(&err)
			Expect(ase).Should(HaveOccurred())
			Expect(ase.Error()).Should(Equal(expectedFmt))
		})
	})

	It("test regular error", func() {
		err := errors.New("test error")
		Expect(err.Error()).Should(Equal("test error"))
		ase := GetAssistedError(err)
		Expect(ase).Should(HaveOccurred())
		Expect(ase.Error()).Should(Equal("test error"))
	})
})
