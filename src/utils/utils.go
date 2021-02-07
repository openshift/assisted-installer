package utils

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/openshift/assisted-service/pkg/requestid"

	"github.com/pkg/errors"

	"github.com/openshift/assisted-installer-agent/pkg/journalLogger"
	"golang.org/x/net/http/httpproxy"

	ignition "github.com/coreos/ignition/v2/config/v3_1"
	"github.com/openshift/assisted-service/models"

	"github.com/hashicorp/go-version"
	"github.com/sirupsen/logrus"
	"github.com/vincent-petithory/dataurl"
)

var (
	envProxyOnce          sync.Once
	envVarsProxyFuncValue func(*url.URL) (*url.URL, error)
)

type WalkMode uint32

const (
	W_FILEONLY WalkMode = iota
	W_DIRONLY
	W_ALL
)

func (wm WalkMode) IncludeFiles() bool {
	return wm == W_FILEONLY || wm == W_ALL
}

func (wm WalkMode) IncludeDirs() bool {
	return wm == W_DIRONLY || wm == W_ALL
}

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

func FindFiles(root string, mode WalkMode, pattern string) ([]string, error) {
	var matches []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if !(info.IsDir() && mode.IncludeDirs() || !info.IsDir() && mode.IncludeFiles()) {
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

func WaitForPredicate(timeout time.Duration, interval time.Duration, predicate func() bool) error {
	timeoutAfter := time.After(timeout)
	ticker := time.NewTicker(interval)
	// Keep trying until we're time out or get true
	for {
		select {
		// Got a timeout! fail with a timeout error
		case <-timeoutAfter:
			return errors.New("timed out")
		// Got a tick, we should check on checkSomething()
		case <-ticker.C:
			if predicate() {
				return nil
			}
		}
	}
}

// ProxyFromEnvVars provides an alternative to http.ProxyFromEnvironment since it is being initialized only
// once and that happens by k8s before proxy settings was obtained. While this is no issue for k8s, it prevents
// any out-of-cluster traffic from using the proxy
func ProxyFromEnvVars(req *http.Request) (*url.URL, error) {
	return envVarsProxyFunc()(req.URL)
}

func envVarsProxyFunc() func(*url.URL) (*url.URL, error) {
	envProxyOnce.Do(func() {
		config := &httpproxy.Config{
			HTTPProxy:  os.Getenv("HTTP_PROXY"),
			HTTPSProxy: os.Getenv("HTTPS_PROXY"),
			NoProxy:    os.Getenv("NO_PROXY"),
			CGI:        os.Getenv("REQUEST_METHOD") != "",
		}
		envVarsProxyFuncValue = config.ProxyFunc()
	})
	return envVarsProxyFuncValue
}

func SetNoProxyEnv(noProxy string) {
	os.Setenv("NO_PROXY", noProxy)
	os.Setenv("no_proxy", noProxy)
}

func GenerateRequestContext() context.Context {
	return requestid.ToContext(context.Background(), requestid.NewID())
}

func RequestIDLogger(ctx context.Context, log *logrus.Logger) logrus.FieldLogger {
	return requestid.RequestIDLogger(log, requestid.FromContext(ctx))
}

func IsVersionLessThan47(openshiftVersion string) (bool, error) {
	clusterVersion, err := version.NewVersion(openshiftVersion)
	if err != nil {
		return false, err
	}
	v47, err := version.NewVersion("4.7")
	if err != nil {
		return false, err
	}

	return clusterVersion.LessThan(v47), nil
}

func EtcdPatchRequired(openshiftVersion string) (bool, error) {
	return IsVersionLessThan47(openshiftVersion)
}
