package utils

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

func InitLogger(verbose bool) *logrus.Logger {
	var log = logrus.New()
	// log to console and file
	f, err := os.OpenFile("crawler.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
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
