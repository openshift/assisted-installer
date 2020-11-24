package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/ioutil"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

var _ = Describe("tar_utils", func() {
	var (
		l = logrus.New()
	)
	l.SetOutput(ioutil.Discard)
	Context("tar utils", func() {
		It("test multiple input sources", func() {
			var outbuf bytes.Buffer
			buf := []byte("This is a test string")
			By("tar gz buffer and file together into in-memory buffer")
			br := bytes.NewReader(buf)
			h1 := NewTarEntry(br, nil, int64(br.Len()), "f1.log")
			h2, err := NewTarEntryFromFile("../../test_files/tartest.tar.gz")
			Expect(err).NotTo(HaveOccurred())

			err = WriteToTarGz(&outbuf, []TarEntry{*h1, *h2})
			Expect(err).NotTo(HaveOccurred())

			By("verify tar gz structure")
			var content bytes.Buffer
			var tarcontent bytes.Buffer
			zr, _ := gzip.NewReader(&outbuf)
			_, _ = io.Copy(&tarcontent, zr)
			tr := tar.NewReader(&tarcontent)

			hdr, err := tr.Next()
			Expect(err).NotTo(HaveOccurred())
			Expect(hdr.Name).To(Equal("f1.log"))
			_, _ = io.Copy(&content, tr)
			Expect(content.String()).To(Equal("This is a test string"))

			var filecontent bytes.Buffer
			var filetarcontent bytes.Buffer
			var filezipcontent bytes.Buffer
			hdr, err = tr.Next()
			Expect(err).NotTo(HaveOccurred())
			Expect(hdr.Name).To(Equal("tartest.tar.gz"))
			_, _ = io.Copy(&filezipcontent, tr)
			filezr, _ := gzip.NewReader(&filezipcontent)
			_, _ = io.Copy(&filetarcontent, filezr)
			filetr := tar.NewReader(&filetarcontent)
			_, _ = filetr.Next()
			_, _ = io.Copy(&filecontent, filetr)
			Expect(filecontent.String()).To(Equal("This is an example file for tar tests\n"))
		})
	})

})
