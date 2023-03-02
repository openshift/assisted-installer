package utils

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

type TarEntry struct {
	Header *tar.Header
	Reader io.Reader
	Closer io.Closer
}

func NewTarEntry(reader io.Reader, closer io.Closer, sizeOfData int64, fileName string) *TarEntry {
	return &TarEntry{
		Header: &tar.Header{
			Name:    fileName,
			Size:    sizeOfData,
			Mode:    0644,
			ModTime: time.Now(),
		},
		Reader: reader,
		Closer: closer,
	}
}

func NewTarEntryFromFile(path string) (*TarEntry, error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	fi, err := fd.Stat()
	if err != nil {
		return nil, err
	}

	return NewTarEntry(bufio.NewReader(fd), fd, fi.Size(), fi.Name()), nil
}

// We should try to send data even if one of the tarEntries fails
func WriteToTarGz(w io.Writer, entries []TarEntry, log logrus.FieldLogger) error {
	// now lets create the header as needed for this file within the tarball
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, tarEntry := range entries {
		log.Infof("Uploading to tar %s", tarEntry.Header.Name)
		// write the header to the tarball archive
		header := *tarEntry.Header
		if err := tw.WriteHeader(&header); err != nil {
			log.WithError(err).Errorf("Failed to write header to %s", tarEntry.Header.Name)
			continue
		}
		// copy the file data to the tarball
		if tarEntry.Closer != nil {
			defer tarEntry.Closer.Close()
		}
		if _, err := io.Copy(tw, tarEntry.Reader); err != nil {
			log.WithError(err).Errorf("Failed to copy %s", tarEntry.Header.Name)
			continue
		}
	}
	return nil
}
