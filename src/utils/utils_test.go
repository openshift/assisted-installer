package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
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
	l.SetOutput(io.Discard)
	Context("Verify ignition parsing for pull secret", func() {
		It("test GetPullSecretFromIgnition", func() {
			ignitionData, err := os.ReadFile("../../test_files/test_ignition.ign")
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
			found, err := FindFiles("../../test_files", W_FILEONLY, "*.json")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(found)).Should(Equal(1))
			Expect(filepath.Base(found[0])).Should(Equal("node.json"))

			found, err = FindFiles("../../test_files", W_FILEONLY, "*.not_exists")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(found)).Should(Equal(0))

			_, err = FindFiles("../../test_files_not_exists", W_FILEONLY, "*.json")
			Expect(err).Should(HaveOccurred())
		})

		It("Read directory and return matched dir", func() {
			found, err := FindFiles("../../test_files", W_DIRONLY, "dir_*")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(found)).Should(Equal(1))
			Expect(filepath.Base(found[0])).Should(Equal("dir_for_test"))
		})

		It("Read directory and return all matches", func() {
			found, err := FindFiles("../../test_files", W_ALL, "*test*")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(found)).Should(Equal(3))
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

var _ = Describe("CombineErrors", func() {
	var (
		error1       = errors.New("One")
		error2       = errors.New("Two")
		textCombined = fmt.Sprintf("%s", errors.Wrapf(error1, "%s", error2))
	)
	It("When both errors are nil, return nil", func() {
		err := CombineErrors(nil, nil)
		Expect(err).Should(BeNil())
	})
	It("When error1 is nil, return error2", func() {
		err := CombineErrors(nil, error2)
		Expect(err).Should(Equal(error2))
	})
	It("When both errors exist, combine them", func() {
		err := CombineErrors(error1, error2)
		textErr := fmt.Sprintf("%s", err)
		Expect(textErr).Should(Equal(textCombined))
	})
})
