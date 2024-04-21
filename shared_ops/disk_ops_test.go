package shared_ops

import (
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"

	gomock "go.uber.org/mock/gomock"
)

func TestDiskOps(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "installer_test")
}

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

var _ = Describe("GetVolumeGroupsByDisk", func() {

	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *MockExecute
		d        DiskOps
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = NewMockExecute(ctrl)
		d = NewDiskOps(l, execMock)
	})

	It("When volume groups are available for a given disk, they should be returned", func() {
		m := MatcherContainsStringElements{[]string{"--noheadings", "-o", "vg_name,pv_name"}, true}
		mockedVgsResult := `vg0 /dev/sda
		vg1 /dev/sdb
		vg2 /dev/sdx
		vg3 /dev/sdx`
		execMock.EXPECT().Execute("vgs", m).Times(1).Return(mockedVgsResult, nil)
		result, err := d.GetVolumeGroupsByDisk("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
		Expect(len(result)).To(Equal(2))
		Expect(result[0]).To(Equal("vg2"))
		Expect(result[1]).To(Equal("vg3"))
	})

	It("When no volume groups are available for a given group, none should be returned", func() {
		m := MatcherContainsStringElements{[]string{"--noheadings", "-o", "vg_name,pv_name"}, true}
		mockedVgsResult := `vg0 /dev/sda
		vg1 /dev/sdb`
		execMock.EXPECT().Execute("vgs", m).Times(1).Return(mockedVgsResult, nil)
		result, err := d.GetVolumeGroupsByDisk("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
		Expect(len(result)).To(Equal(0))
	})

	It("When the command to fetch volume groups returns an error, no groups should be returned", func() {
		m := MatcherContainsStringElements{[]string{"--noheadings", "-o", "vg_name,pv_name"}, true}
		execMock.EXPECT().Execute("vgs", m).Times(1).Return("", errors.New("Some arbitrary error occurred!"))
		result, err := d.GetVolumeGroupsByDisk("/dev/sdx")
		Expect(err).To(HaveOccurred())
		Expect(len(result)).To(Equal(0))
	})
})

var _ = Describe("RemoveAllPVsOnDevice", func() {

	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *MockExecute
		d        DiskOps
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = NewMockExecute(ctrl)
		d = NewDiskOps(l, execMock)
	})

	It("When volume pvs are available for a given disk, they should be removed", func() {
		m := MatcherContainsStringElements{[]string{"--noheadings", "-o", "pv_name"}, true}
		mockedVgsResult := `/dev/sda1
		/dev/sdb1
		/dev/sdx1
		/dev/sdx2`
		execMock.EXPECT().Execute("pvs", m).Times(1).Return(mockedVgsResult, nil)

		removeMatcher := MatcherContainsStringElements{[]string{"/dev/sdx1", "-y", "-ff"}, true}
		execMock.EXPECT().Execute("pvremove", removeMatcher).Times(1).Return("", nil)

		removeMatcher = MatcherContainsStringElements{[]string{"/dev/sdx2", "-y", "-ff"}, true}
		execMock.EXPECT().Execute("pvremove", removeMatcher).Times(1).Return("", nil)

		err := d.RemoveAllPVsOnDevice("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
	})

	It("When no pvs are available for a given disk, nothing should be deleted", func() {
		m := MatcherContainsStringElements{[]string{"--noheadings", "-o", "pv_name"}, true}
		mockedVgsResult := `/dev/sda1
		/dev/sdb`
		execMock.EXPECT().Execute("pvs", m).Times(1).Return(mockedVgsResult, nil)
		err := d.RemoveAllPVsOnDevice("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
	})

	It("When the command to fetch pvs returns an error, error should be returned", func() {
		m := MatcherContainsStringElements{[]string{"--noheadings", "-o", "pv_name"}, true}
		execMock.EXPECT().Execute("pvs", m).Times(1).Return("", errors.New("Some arbitrary error occurred!"))
		err := d.RemoveAllPVsOnDevice("/dev/sdx")
		Expect(err).To(HaveOccurred())
	})

	It("When remove pvs returns an error, error should be returned", func() {
		m := MatcherContainsStringElements{[]string{"--noheadings", "-o", "pv_name"}, true}
		mockedVgsResult := `/dev/sda1
		/dev/sdb1
		/dev/sdx1
		/dev/sdx2`
		execMock.EXPECT().Execute("pvs", m).Times(1).Return(mockedVgsResult, nil)

		removeMatcher := MatcherContainsStringElements{[]string{"/dev/sdx1", "-y", "-ff"}, true}
		execMock.EXPECT().Execute("pvremove", removeMatcher).Times(1).Return("", nil)

		removeMatcher = MatcherContainsStringElements{[]string{"/dev/sdx2", "-y", "-ff"}, true}
		execMock.EXPECT().Execute("pvremove", removeMatcher).Times(1).Return("", errors.New("Some arbitrary error occurred!"))

		err := d.RemoveAllPVsOnDevice("/dev/sdx")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("RemoveAllDMDevicesOnDisk", func() {

	var (
		l        = logrus.New()
		ctrl     *gomock.Controller
		execMock *MockExecute
		d        DiskOps
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		execMock = NewMockExecute(ctrl)
		d = NewDiskOps(l, execMock)
	})

	It("When DM devices are available for a given disk, they should be removed", func() {
		dmsetupLsMatcher := MatcherContainsStringElements{[]string{"ls"}, true}
		mockedDmsetupLsResult := `volumegroup-logicalvolume	(253:0)`
		execMock.EXPECT().Execute("dmsetup", dmsetupLsMatcher).Times(1).Return(mockedDmsetupLsResult, nil)

		dmsetupDepsMatcher := MatcherContainsStringElements{[]string{"deps", "-o", "devname", "volumegroup-logicalvolume"}, true}
		mockedDmsetupDepsResult := `1 dependencies  : (sdx1)`
		execMock.EXPECT().Execute("dmsetup", dmsetupDepsMatcher).Times(1).Return(mockedDmsetupDepsResult, nil)

		removeMatcher := MatcherContainsStringElements{[]string{"remove", "--retry", "volumegroup-logicalvolume"}, true}
		execMock.EXPECT().Execute("dmsetup", removeMatcher).Times(1).Return("", nil)

		err := d.RemoveAllDMDevicesOnDisk("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
	})

	It("When no DM devices are available for a given disk, nothing should be deleted", func() {
		dmsetupLsMatcher := MatcherContainsStringElements{[]string{"ls"}, true}
		mockedDmsetupLsResult := `volumegroup-logicalvolume	(253:0)`
		execMock.EXPECT().Execute("dmsetup", dmsetupLsMatcher).Times(1).Return(mockedDmsetupLsResult, nil)

		dmsetupDepsMatcher := MatcherContainsStringElements{[]string{"deps", "-o", "devname", "volumegroup-logicalvolume"}, true}
		mockedDmsetupDepsResult := `1 dependencies  : (vdb1)`
		execMock.EXPECT().Execute("dmsetup", dmsetupDepsMatcher).Times(1).Return(mockedDmsetupDepsResult, nil)

		err := d.RemoveAllDMDevicesOnDisk("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
	})

	It("When no DM devices are available for a given disk, nothing should be done", func() {
		dmsetupLsMatcher := MatcherContainsStringElements{[]string{"ls"}, true}
		mockedDmsetupLsResult := `No devices found`
		execMock.EXPECT().Execute("dmsetup", dmsetupLsMatcher).Times(1).Return(mockedDmsetupLsResult, nil)

		err := d.RemoveAllDMDevicesOnDisk("/dev/sdx")
		Expect(err).ToNot(HaveOccurred())
	})

	It("When the command to list DM devices returns an error, error should be returned", func() {
		dmsetupLsMatcher := MatcherContainsStringElements{[]string{"ls"}, true}
		execMock.EXPECT().Execute("dmsetup", dmsetupLsMatcher).Times(1).Return("", errors.New("Some arbitrary error occurred!"))

		err := d.RemoveAllDMDevicesOnDisk("/dev/sdx")
		Expect(err).To(HaveOccurred())
	})

	It("When the command to list DM device dependencies returns an error, error should be returned", func() {
		dmsetupLsMatcher := MatcherContainsStringElements{[]string{"ls"}, true}
		mockedDmsetupLsResult := `volumegroup-logicalvolume	(253:0)`
		execMock.EXPECT().Execute("dmsetup", dmsetupLsMatcher).Times(1).Return(mockedDmsetupLsResult, nil)

		dmsetupDepsMatcher := MatcherContainsStringElements{[]string{"deps", "-o", "devname", "volumegroup-logicalvolume"}, true}
		execMock.EXPECT().Execute("dmsetup", dmsetupDepsMatcher).Times(1).Return("", errors.New("Some arbitrary error occurred!"))

		err := d.RemoveAllDMDevicesOnDisk("/dev/sdx")
		Expect(err).To(HaveOccurred())
	})

	It("When the command to remove DM device returns an error, error should be returned", func() {
		dmsetupLsMatcher := MatcherContainsStringElements{[]string{"ls"}, true}
		mockedDmsetupLsResult := `volumegroup-logicalvolume	(253:0)`
		execMock.EXPECT().Execute("dmsetup", dmsetupLsMatcher).Times(1).Return(mockedDmsetupLsResult, nil)

		dmsetupDepsMatcher := MatcherContainsStringElements{[]string{"deps", "-o", "devname", "volumegroup-logicalvolume"}, true}
		mockedDmsetupDepsResult := `1 dependencies  : (sdx1)`
		execMock.EXPECT().Execute("dmsetup", dmsetupDepsMatcher).Times(1).Return(mockedDmsetupDepsResult, nil)

		removeMatcher := MatcherContainsStringElements{[]string{"remove", "--retry", "volumegroup-logicalvolume"}, true}
		execMock.EXPECT().Execute("dmsetup", removeMatcher).Times(1).Return("", errors.New("Some arbitrary error occurred!"))

		err := d.RemoveAllDMDevicesOnDisk("/dev/sdx")
		Expect(err).To(HaveOccurred())
	})

})
