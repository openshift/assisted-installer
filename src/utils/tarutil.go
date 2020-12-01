package utils

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"time"
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

func WriteToTarGz(w io.Writer, entries []TarEntry) error {
	// now lets create the header as needed for this file within the tarball
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, tarEntry := range entries {
		// write the header to the tarball archive
		header := *tarEntry.Header
		if err := tw.WriteHeader(&header); err != nil {
			return err
		}
		// copy the file data to the tarball
		if tarEntry.Closer != nil {
			defer tarEntry.Closer.Close()
		}
		if _, err := io.Copy(tw, tarEntry.Reader); err != nil {
			return err
		}
	}
	return nil
}
