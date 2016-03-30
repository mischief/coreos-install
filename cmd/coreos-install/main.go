package main

import (
	"bytes"
	"compress/bzip2"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
)

var (
	device    = flag.String("d", "", "Install CoreOS to the given device.")
	version   = flag.String("V", "current", "Version to install (e.g. current)")
	channel   = flag.String("C", "stable", "Release channel to use (e.g. beta)")
	oem       = flag.String("o", "", "OEM type to install (e.g. ami)")
	cloudinit = flag.String("c", "", "Insert a cloud-init config to be executed on boot.")
	ignition  = flag.String("i", "", "Insert an Ignition config to be executed on boot.")
	tempdir   = flag.String("t", "", "Temporary location with enough space to download images.")
	verbose   = flag.Bool("v", false, "Super verbose, for debugging.")
	baseurl   = flag.String("b", "", "URL to the image mirror")
	copynet   = flag.Bool("n", false, "Copy generated network units to the root partition.")
)

func die(format string, why ...interface{}) {
	fmt.Fprintf(os.Stderr, format, why...)
	os.Exit(1)
}

func main() {
	flag.Parse()

	if *device == "" {
		die("No target block device provided, -d is required.")
	}

	// TODO: check dev type

	if *cloudinit != "" {
		if _, err := os.Stat(*cloudinit); err != nil {
			die("Cloud config file %q is inaccessible: %v", *cloudinit, err)
		}

		// TODO: validate cloud-config
	}

	if *ignition != "" {
		if _, err := os.Stat(*ignition); err != nil {
			die("Ignition config file %q is inaccessible: %v", *cloudinit, err)
		}

		// TODO: validate cloud-config
	}

	imagename := "coreos_production_image.bin.bz2"
	if *oem != "" {
		imagename = fmt.Sprintf("coreos_production_%s_image.bin.bz2", *oem)
	}

	if *baseurl == "" {
		*baseurl = fmt.Sprintf("http://%s.release.core-os.net/amd64-usr", *channel)
	}

	imageurl := fmt.Sprintf("%s/%s/%s", *baseurl, *version, imagename)
	sigurl := imageurl + ".sig"

	cl := http.DefaultClient

	r, err := cl.Head(imageurl)
	if err != nil {
		die("Failed to check image url: %v", err)
	}

	if r.StatusCode != http.StatusOK {
		die("Image URL unavailable: %s", imageurl)
	}

	r.Body.Close()

	fmt.Printf("Downloading the signature for %s...\n", imageurl)

	r, err = cl.Get(sigurl)
	if err != nil {
		die("Failed to check signature url: %v", err)
	}

	if r.StatusCode != http.StatusOK {
		die("Signature URL unavailable: %s", sigurl)
	}

	signature := new(bytes.Buffer)
	io.Copy(signature, r.Body)
	r.Body.Close()

	fmt.Printf("Downloading, writing and verifying %s...\n", imageurl)

	r, err = cl.Get(imageurl)
	if err != nil {
		die("Failed to get image url: %v", err)
	}

	if r.StatusCode != http.StatusOK {
		die("Image URL unavailable: %s", sigurl)
	}

	bzippr, bzippw := io.Pipe()
	gpgreader := io.TeeReader(r.Body, bzippw)
	bzipreader := bzip2.NewReader(bzippr)

	blockwriter, err := os.OpenFile(*device, os.O_WRONLY, 0)
	if err != nil {
		die("Failed to open %q for writing: %v", *device, err)
	}
	defer blockwriter.Close()

	var wg sync.WaitGroup

	wg.Add(1)
	go func(r, sigr io.Reader, pw io.WriteCloser) {
		if err := verify(r, sigr); err != nil {
			die("GPG verification failed: %v", err)
		}
		wg.Done()

		// XXX: close the bzip reader once the gpg reader got eof.
		pw.Close()
	}(gpgreader, signature, bzippw)

	// replace with a disk
	if _, err = io.Copy(blockwriter, bzipreader); err != nil {
		die("Writing disk image failed: %v", err)
	}

	r.Body.Close()
	wg.Wait()
}
