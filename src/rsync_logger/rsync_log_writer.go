package rsync_logger

import (
	"bytes"
	"fmt"
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

type RsyncInstallerLogWriter struct {
	log              logrus.FieldLogger
	lastLogLine      []byte
	progressReporter inventory_client.InventoryClient
	progressRegex    *regexp.Regexp
	infraEnvID       string
	hostID           string
	lastProgress     int
	hostStage        *models.HostStage
}

func NewRsyncInstallerLogWriter(logger logrus.FieldLogger, progressReporter inventory_client.InventoryClient,
	infraEnvID string, hostID string, hostStage *models.HostStage) *RsyncInstallerLogWriter {
	return &RsyncInstallerLogWriter{log: logger,
		lastLogLine:      []byte{},
		progressReporter: progressReporter,
		progressRegex:    regexp.MustCompile(`\s*(\S+)\s+(\d+)%\s+(\S+)(?:\s+\([^)]+\))?`),
		infraEnvID:       infraEnvID,
		hostID:           hostID,
		lastProgress:     0,
		hostStage:        hostStage,
	}
}

func (l *RsyncInstallerLogWriter) Write(p []byte) (n int, err error) {
	// Append bytes to last log line slice
	l.lastLogLine = append(l.lastLogLine, p...)
	if bytes.Contains(l.lastLogLine, []byte{'\n'}) || bytes.Contains(l.lastLogLine, []byte{'\r'}) {
		line := strings.TrimSpace(string(l.lastLogLine))
		l.reportProgress(line)
		l.lastLogLine = []byte{}
	}
	return len(p), nil
}

func (l *RsyncInstallerLogWriter) reportProgress(line string) {
	match := l.progressRegex.FindStringSubmatch(line)
	if len(match) < 4 {
		// Not a progress line (need: full match + size + percentage + speed)
		return
	}

	// Extract first progress entry
	// Format: "size percentage speed" (time excluded)
	firstProgressEntry := fmt.Sprintf("%s %s%% %s", match[1], match[2], match[3])

	currentPercent, err := strconv.Atoi(match[2])
	if err != nil {
		// Do nothing in case we fail to parse the log line
		return
	}
	if l.lastProgress == completed {
		// Already completed - skip duplicate 100% lines
		return
	}
	if currentPercent >= l.lastProgress+MinProgressDelta || currentPercent == completed {
		// If the progress is more than 5% report it
		ctx := utils.GenerateRequestContext()
		percentStr := fmt.Sprintf("%d%%", currentPercent)
		var err error
		if l.hostStage != nil {
			if err = l.progressReporter.UpdateHostInstallProgress(ctx, l.infraEnvID, l.hostID, *l.hostStage, percentStr); err != nil {
				l.log.Errorf("failed to update host install progress: %v", err)
				return
			}
		}
		l.lastProgress = currentPercent
		l.log.Infof("rsync progress: %s", firstProgressEntry)
	}
}
