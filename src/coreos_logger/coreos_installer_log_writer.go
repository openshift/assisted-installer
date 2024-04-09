package coreos_logger

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/openshift/assisted-installer/src/utils"

	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
)

const MinProgressDelta = 5
const completed = 100

type CoreosInstallerLogWriter struct {
	log              logrus.FieldLogger
	lastLogLine      []byte
	progressReporter inventory_client.InventoryClient
	progressRegex    *regexp.Regexp
	infraEnvID       string
	hostID           string
	lastProgress     int
}

func NewCoreosInstallerLogWriter(logger logrus.FieldLogger, progressReporter inventory_client.InventoryClient, infraEnvID string, hostID string) *CoreosInstallerLogWriter {
	return &CoreosInstallerLogWriter{log: logger,
		lastLogLine:      []byte{},
		progressReporter: progressReporter,
		progressRegex:    regexp.MustCompile(`(.*?)\((.*?\%)\)\s*`),
		infraEnvID:       infraEnvID,
		hostID:           hostID,
		lastProgress:     0,
	}
}

func (l *CoreosInstallerLogWriter) Write(p []byte) (n int, err error) {
	// Append bytes to last log line slice
	l.lastLogLine = append(l.lastLogLine, p...)
	if bytes.Contains(l.lastLogLine, []byte{'\r'}) || bytes.Contains(l.lastLogLine, []byte{'\n'}) {
		// If log contains new line or carriage return - log it and set to empty slice
		l.log.Info(string(l.lastLogLine))
		l.reportProgress()
		l.lastLogLine = []byte{}
	}
	return len(p), nil
}

func (l *CoreosInstallerLogWriter) reportProgress() {
	match := l.progressRegex.FindStringSubmatch(string(l.lastLogLine))
	if len(match) < 3 {
		return
	}
	currentPercent, err := strconv.Atoi(strings.TrimRight(match[2], "%"))
	// in case we fail to parse the log line we do nothing
	if err != nil {
		return
	}
	if currentPercent >= l.lastProgress+MinProgressDelta || (currentPercent == completed && l.lastProgress != completed) {
		// If the progress is more than 5% report it
		ctx := utils.GenerateRequestContext()
		if err := l.progressReporter.UpdateHostInstallProgress(ctx, l.infraEnvID, l.hostID, models.HostStageWritingImageToDisk, match[2]); err == nil {
			l.lastProgress = currentPercent
		}
	}
}
