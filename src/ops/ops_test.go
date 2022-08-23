package ops

import (
	"reflect"

	"errors"

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

var _ = Describe("GetVolumeGroupsByDisk", func() {

	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *execute.MockExecute
		conf     *config.Config
		o        Ops
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = execute.NewMockExecute(ctrl)
		conf = &config.Config{}
		o = NewOpsWithConfig(conf, l, execMock)
	})

	It("When volume groups are available for a given disk, they should be returned", func() {
		m := MatcherContainsStringElements{[]string{"vgs", "--noheadings", "-o", "vg_name,pv_name"}, true}
		mockedVgsResult := `vg0 /dev/sda
		vg1 /dev/sdb
		vg2 /dev/sdx
		vg3 /dev/sdx`
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m).Times(1).Return(mockedVgsResult, nil)
		result, err := o.GetVolumeGroupsByDisk("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
		Expect(len(result)).To(Equal(2))
		Expect(result[0]).To(Equal("vg2"))
		Expect(result[1]).To(Equal("vg3"))
	})

	It("When no volume groups are available for a given group, none should be returned", func() {
		m := MatcherContainsStringElements{[]string{"vgs", "--noheadings", "-o", "vg_name,pv_name"}, true}
		mockedVgsResult := `vg0 /dev/sda
		vg1 /dev/sdb`
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m).Times(1).Return(mockedVgsResult, nil)
		result, err := o.GetVolumeGroupsByDisk("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
		Expect(len(result)).To(Equal(0))
	})

	It("When the command to fetch volume groups returns an error, no groups should be returned", func() {
		m := MatcherContainsStringElements{[]string{"vgs", "--noheadings", "-o", "vg_name,pv_name"}, true}
		execMock.EXPECT().ExecCommand(gomock.Any(), gomock.Any(), m).Times(1).Return("", errors.New("Some arbitrary error occurred!"))
		result, err := o.GetVolumeGroupsByDisk("/dev/sdx")
		Expect(err).To(HaveOccurred())
		Expect(len(result)).To(Equal(0))
	})
})
