package ops

import (
	"reflect"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/openshift/assisted-installer/src/ops/execute"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
)

var _ = Describe("installerArgs", func() {
	var (
		device       = "/dev/sda"
		ignitionPath = "/tmp/ignition.ign"
	)

	It("Returns the correct list with no extra args", func() {
		args := installerArgs(ignitionPath, device, nil)
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})

	It("Returns the correct list with empty extra args", func() {
		args := installerArgs(ignitionPath, device, []string{})
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})

	It("Returns the correct list with extra args", func() {
		args := installerArgs(ignitionPath, device, []string{"-n", "--append-karg", "nameserver=8.8.8.8"})
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "-n", "--append-karg", "nameserver=8.8.8.8", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})
})

type MatcherContainsStringElements struct {
	Elements    []string
	ShouldMatch bool
}

func (o MatcherContainsStringElements) Matches(x interface{}) bool {
	switch reflect.TypeOf(x).Kind() {
	case reflect.Array, reflect.Slice:
		break
	default:
		return false
	}

	for _, e := range o.Elements {
		contains := funk.Contains(x, e)
		if !contains && o.ShouldMatch {
			return false
		} else if contains && !o.ShouldMatch {
			return false
		}
	}
	return true
}

func (o MatcherContainsStringElements) String() string {
	if o.ShouldMatch {
		return "All given elements should be in provided array"
	}
	return "All given elements should not be in provided array"
}

var _ = Describe("Upload logs", func() {
	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		conf     *config.Config
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		conf = &config.Config{}
	})

	It("Upload logs with ca path", func() {
		conf = &config.Config{CACertPath: "test.ca"}
		m := MatcherContainsStringElements{[]string{"test.ca:test.ca", "-cacert=test.ca"}, true}
		o := NewOpsWithConfig(conf, l, execMock)
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m).Times(1)
		_, err := o.UploadInstallationLogs(true)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Upload logs without ca path", func() {
		m := MatcherContainsStringElements{[]string{"test.ca:test.ca", "-cacert=test.ca"}, false}
		o := NewOpsWithConfig(conf, l, execMock)
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m).Times(1)
		_, err := o.UploadInstallationLogs(true)
		Expect(err).ToNot(HaveOccurred())
	})
})
