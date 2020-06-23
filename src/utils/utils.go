package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/eranco74/assisted-installer/src/inventory_client"

	ignition "github.com/coreos/ignition/config/v2_2"
	"github.com/vincent-petithory/dataurl"

	"github.com/sirupsen/logrus"
)

const MinProgressDelta = 5

type LogWriter struct {
	log *logrus.Logger
}

func (l *LogWriter) Write(p []byte) (n int, err error) {
	l.log.Info(string(p))
	return len(p), nil
}

func NewLogWriter(logger *logrus.Logger) *LogWriter {
	return &LogWriter{logger}
}

func InitLogger(verbose bool) *logrus.Logger {
	var log = logrus.New()
	// log to console and file
	f, err := os.OpenFile("/var/log/assisted-installer.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}
	wrt := io.MultiWriter(os.Stdout, f)
	log.SetOutput(wrt)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	}
	return log
}

type CoreosInstallerLogWriter struct {
	log              *logrus.Logger
	lastLogLine      []byte
	progressReporter inventory_client.InventoryClient
	progressRegex    *regexp.Regexp
	hostID           string
	lastProgress     int
}

func NewCoreosInstallerLogWriter(logger *logrus.Logger, progressReporter inventory_client.InventoryClient, hostID string) *CoreosInstallerLogWriter {
	return &CoreosInstallerLogWriter{log: logger,
		lastLogLine:      []byte{},
		progressReporter: progressReporter,
		progressRegex:    regexp.MustCompile(`^>(.*?)\((.*?)\)\s*\r`),
		hostID:           hostID,
		lastProgress:     0,
	}
}

func (l *CoreosInstallerLogWriter) Write(p []byte) (n int, err error) {
	if bytes.Contains(p, []byte{'\n'}) {
		// If log has a new line - log it
		l.log.Info(string(p))
	} else {
		// Append bytes to last log line slice
		l.lastLogLine = append(l.lastLogLine, p...)
		if bytes.Contains(l.lastLogLine, []byte{'\r'}) {
			// If log contains carriage return - log it and set to empty slice
			l.log.Info(string(l.lastLogLine))
			l.reportProgress()
			l.lastLogLine = []byte{}

		}
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
	if currentPercent >= l.lastProgress+MinProgressDelta {
		// If the progress is more than 5% report it
		if err := l.progressReporter.UpdateHostStatus(fmt.Sprintf("Writing image to disk - %s", match[2]), l.hostID); err == nil {
			l.lastProgress = currentPercent
		}
	}
}

func GetFileContentFromIgnition(ignitionData []byte, fileName string) ([]byte, error) {
	bm, _, err := ignition.Parse(ignitionData)
	if err != nil {
		return nil, err
	}
	for i := range bm.Storage.Files {
		if bm.Storage.Files[i].Path == fileName {
			pullSecret, err := dataurl.DecodeString(bm.Storage.Files[i].Contents.Source)
			if err != nil {
				return nil, err
			}
			return pullSecret.Data, nil
		}
	}
	return nil, fmt.Errorf("path %s found in ignition", fileName)
}

func GetListOfFilesFromFolder(root, pattern string) ([]string, error) {
	var matches []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if matched, err := filepath.Match(pattern, filepath.Base(path)); err != nil {
			return err
		} else if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func CopyFile(source string, dest string) error {
	from, err := os.Open(source)
	if err != nil {
		return err
	}
	defer from.Close()

	to, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer to.Close()

	_, err = io.Copy(to, from)
	if err != nil {
		return err
	}
	return nil
}

func FindAndRemoveElementFromStringList(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func Retry(attempts int, sleep time.Duration, log *logrus.Logger, f func() error) (err error) {
	for i := 0; i < attempts; i++ {
		err = f()
		if err == nil {
			return
		}
		time.Sleep(sleep)
		log.Warnf("Retrying after error: %s", err)
	}
	return fmt.Errorf("failed after %d attempts, last error: %s", attempts, err)
}
