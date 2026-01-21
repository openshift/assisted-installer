package rsync_logger

import (
	"io"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	gomock "go.uber.org/mock/gomock"
)

var _ = Describe("Verify RsyncInstallerLogger", func() {
	var (
		l = logrus.New()
	)
	l.SetOutput(io.Discard)
	Context("test rsyncInstallerLogger", func() {
		var (
			rilogger      *RsyncInstallerLogWriter
			hook          *test.Hook
			logger        *logrus.Logger
			mockbmclient  *inventory_client.MockInventoryClient
			hostStage     models.HostStage
			testHostStage = "Copying registry data to disk"
		)

		updateProgressSuccess := func(stages [][]string) {
			for _, stage := range stages {
				if len(stage) == 2 {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), "infraEnvID", "hostID", models.HostStage(stage[0]), stage[1]).Return(nil).Times(1)
				} else {
					mockbmclient.EXPECT().UpdateHostInstallProgress(gomock.Any(), "infraEnvID", "hostID", models.HostStage(stage[0]), "").Return(nil).Times(1)
				}
			}
		}

		BeforeEach(func() {
			logger, hook = test.NewNullLogger()
			ctrl := gomock.NewController(GinkgoT())
			mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
			hostStage = models.HostStage(testHostStage)
			rilogger = NewRsyncInstallerLogWriter(logger, mockbmclient, "infraEnvID", "hostID", &hostStage)
		})
		It("test log with new line", func() {
			_, err := rilogger.Write([]byte("some log with a new line \n"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(0))
		})
		It("test full progress line", func() {
			updateProgressSuccess([][]string{{testHostStage, "56%"}})
			_, err := rilogger.Write([]byte("  11.5G  56%   84.95MB/s    0:01:51  \r"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(1))
			Expect(hook.Entries[0].Message).Should(ContainSubstring("rsync progress:"))
			Expect(hook.Entries[0].Message).Should(ContainSubstring("56%"))
		})
		It("test partial line", func() {
			_, err := rilogger.Write([]byte("11.5G"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(0))
		})
		It("test multiple lines", func() {
			updateProgressSuccess([][]string{
				{testHostStage, "55%"},
				{testHostStage, "60%"},
				{testHostStage, "66%"},
			})
			testLogs := []string{
				"  10.5G  55%   80.00MB/s    0:01:55  \r",
				"  10.6G  55%   80.00MB/s    0:01:50  \r",
				"  10.7G  55%   80.00MB/s    0:01:45  \r",
				"  10.8G  60%   80.00MB/s    0:01:40  \r",
				"  11.2G  66%   80.00MB/s    0:01:25  \r",
			}
			for i := range testLogs {
				_, err := rilogger.Write([]byte(testLogs[i]))
				Expect(err).Should(BeNil())
			}
			Expect(len(hook.Entries)).Should(Equal(3))
		})
		It("test multiple lines with multiple 100%", func() {
			updateProgressSuccess([][]string{
				{testHostStage, "90%"},
				{testHostStage, "95%"},
				{testHostStage, "100%"},
			})
			testLogs := []string{
				"  19.6G  90%   80.00MB/s    0:00:05  \r",
				"  19.8G  95%   80.00MB/s    0:00:02  \r",
				"  20G  100%   80.00MB/s    0:00:00  \r",
				"  20G  100%   80.00MB/s    0:00:00  \r",
			}
			for i := range testLogs {
				_, err := rilogger.Write([]byte(testLogs[i]))
				Expect(err).Should(BeNil())
			}
			Expect(len(hook.Entries)).Should(Equal(3))
		})
		It("test non-progress line is not logged", func() {
			_, err := rilogger.Write([]byte("some informational message\n"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(0))
		})
		It("test empty line is not logged", func() {
			_, err := rilogger.Write([]byte("   \r\n"))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(0))
		})
		It("test multiple progress entries in one line - only first is logged", func() {
			updateProgressSuccess([][]string{
				{testHostStage, "100%"},
			})
			// Line with multiple progress entries separated by \r
			line := "21.39G 100%   85.21MB/s    0:03:59 (xfr#1653, to-chk=0/4137)\r         21.39G 100%   85.21MB/s    0:03:59 (xfr#1653, to-chk=0/4137)\r         21.39G 100%   85.21MB/s    0:03:59 (xfr#1653, to-chk=0/4137)\r         21.39G 100%   85.21MB/s    0:03:59 (xfr#1653, to-chk=0/4137)\r         21.39G 100%   85.21MB/s    0:03:59 (xfr#1653, to-chk=0/4137)\r         21.39G 100%   85.21MB/s    0:03:59 (xfr#1653, to-chk=0/4137)"
			_, err := rilogger.Write([]byte(line))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(1))
			// Should log only the first entry in correct format: "size percentage% speed" (time excluded)
			Expect(hook.Entries[0].Message).Should(Equal("rsync progress: 21.39G 100% 85.21MB/s"))
			// Should not contain metadata or subsequent entries
			Expect(hook.Entries[0].Message).ShouldNot(ContainSubstring("xfr#1653"))
			Expect(hook.Entries[0].Message).ShouldNot(ContainSubstring("to-chk"))
			Expect(hook.Entries[0].Message).ShouldNot(ContainSubstring("\r"))
		})
		It("test progress entry with metadata - metadata is excluded", func() {
			updateProgressSuccess([][]string{
				{testHostStage, "75%"},
			})
			line := "15.89G  75%   85.32MB/s    0:02:57 (xfr#445, ir-chk=1000/2131)\r"
			_, err := rilogger.Write([]byte(line))
			Expect(err).Should(BeNil())
			Expect(len(hook.Entries)).Should(Equal(1))
			// Should log only the core progress info without metadata (time excluded)
			Expect(hook.Entries[0].Message).Should(Equal("rsync progress: 15.89G 75% 85.32MB/s"))
			Expect(hook.Entries[0].Message).ShouldNot(ContainSubstring("xfr#445"))
			Expect(hook.Entries[0].Message).ShouldNot(ContainSubstring("ir-chk"))
		})

		Context("MinProgressDelta logic", func() {
			It("should log progress when increase is exactly 5%", func() {
				updateProgressSuccess([][]string{
					{testHostStage, "5%"},
					{testHostStage, "10%"},
				})
				testLogs := []string{
					"  1G  5%   80.00MB/s    0:02:00  \r",
					"  2G  10%   80.00MB/s    0:01:50  \r",
				}
				for i := range testLogs {
					_, err := rilogger.Write([]byte(testLogs[i]))
					Expect(err).Should(BeNil())
				}
				Expect(len(hook.Entries)).Should(Equal(2))
			})
			It("should not log progress when increase is less than 5%", func() {
				updateProgressSuccess([][]string{
					{testHostStage, "5%"},
				})
				testLogs := []string{
					"  1G  5%   80.00MB/s    0:02:00  \r",
					"  1.5G  6%   80.00MB/s    0:01:55  \r",
					"  2G  7%   80.00MB/s    0:01:50  \r",
					"  2.5G  8%   80.00MB/s    0:01:45  \r",
					"  3G  9%   80.00MB/s    0:01:40  \r",
				}
				for i := range testLogs {
					_, err := rilogger.Write([]byte(testLogs[i]))
					Expect(err).Should(BeNil())
				}
				// Only first entry (5%) should be logged, others are < 5% increase
				Expect(len(hook.Entries)).Should(Equal(1))
				Expect(hook.Entries[0].Message).Should(ContainSubstring("5%"))
			})
			It("should log progress when increase is more than 5%", func() {
				updateProgressSuccess([][]string{
					{testHostStage, "5%"},
					{testHostStage, "15%"},
				})
				testLogs := []string{
					"  1G  5%   80.00MB/s    0:02:00  \r",
					"  3G  15%   80.00MB/s    0:01:30  \r",
				}
				for i := range testLogs {
					_, err := rilogger.Write([]byte(testLogs[i]))
					Expect(err).Should(BeNil())
				}
				Expect(len(hook.Entries)).Should(Equal(2))
			})
			It("should always log 100% even if increase is less than 5%", func() {
				updateProgressSuccess([][]string{
					{testHostStage, "98%"},
					{testHostStage, "100%"},
				})
				testLogs := []string{
					"  19G  98%   80.00MB/s    0:00:05  \r",
					"  20G  100%   80.00MB/s    0:00:00  \r",
				}
				for i := range testLogs {
					_, err := rilogger.Write([]byte(testLogs[i]))
					Expect(err).Should(BeNil())
				}
				// Both should be logged: 98% (initial) and 100% (always logged)
				Expect(len(hook.Entries)).Should(Equal(2))
				Expect(hook.Entries[0].Message).Should(ContainSubstring("98%"))
				Expect(hook.Entries[1].Message).Should(ContainSubstring("100%"))
			})
			It("should not log duplicate percentages that don't meet delta", func() {
				updateProgressSuccess([][]string{
					{testHostStage, "10%"},
				})
				testLogs := []string{
					"  2G  10%   80.00MB/s    0:02:00  \r",
					"  2.1G  10%   80.00MB/s    0:01:55  \r",
					"  2.2G  10%   80.00MB/s    0:01:50  \r",
				}
				for i := range testLogs {
					_, err := rilogger.Write([]byte(testLogs[i]))
					Expect(err).Should(BeNil())
				}
				// Only first 10% should be logged
				Expect(len(hook.Entries)).Should(Equal(1))
				Expect(hook.Entries[0].Message).Should(ContainSubstring("10%"))
			})
		})
	})
})
