package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/openshift/assisted-installer-agent/pkg/journalLogger"

	ignition "github.com/coreos/ignition/v2/config/v3_1"
	"github.com/coreos/ignition/v2/config/v3_1/types"
	"github.com/openshift/assisted-service/models"

	"github.com/vincent-petithory/dataurl"

	"github.com/sirupsen/logrus"
)

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

func InitLogger(verbose bool, enableJournal bool) *logrus.Logger {
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
	// log to journal
	if enableJournal {
		journalLogger.SetJournalLogging(log, &journalLogger.JournalWriter{}, map[string]interface{}{
			"TAG": "installer",
		})
	}

	return log
}

func GetFileContentFromIgnition(ignitionData []byte, fileName string) ([]byte, error) {
	bm, _, err := ignition.Parse(ignitionData)
	if err != nil {
		return nil, err
	}
	for i := range bm.Storage.Files {
		if bm.Storage.Files[i].Path == fileName {
			pullSecret, err := dataurl.DecodeString(*bm.Storage.Files[i].Contents.Source)
			if err != nil {
				return nil, err
			}
			return pullSecret.Data, nil
		}
	}
	return nil, fmt.Errorf("path %s found in ignition", fileName)
}

func SetFileInIgnition(ignitionData []byte, filePath, fileContents string, mode int) ([]byte, error) {
	bm, _, err := ignition.Parse(ignitionData)
	if err != nil {
		return nil, err
	}

	rootUser := "root"
	file := types.File{
		Node: types.Node{
			Path:      filePath,
			Overwrite: nil,
			Group:     types.NodeGroup{},
			User:      types.NodeUser{Name: &rootUser},
		},
		FileEmbedded1: types.FileEmbedded1{
			Append: []types.Resource{},
			Contents: types.Resource{
				Source: &fileContents,
			},
			Mode: &mode,
		},
	}
	bm.Storage.Files = append(bm.Storage.Files, file)
	return json.Marshal(bm)
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

func GetHostIpsFromInventory(inventory *models.Inventory) ([]string, error) {
	var ips []string
	for _, netInt := range inventory.Interfaces {
		for _, ip := range append(netInt.IPV4Addresses, netInt.IPV6Addresses...) {
			parsedIp, _, err := net.ParseCIDR(ip)
			if err != nil {
				return nil, err
			}
			ips = append(ips, parsedIp.String())
		}
	}
	return ips, nil
}
