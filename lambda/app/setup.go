package app

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func getTerraform(version string) (string, error) {
	vers := version
	if strings.EqualFold(vers, "latest") {
		var err error
		vers, err = latest()
		if err != nil {
			return "", err
		}
	}

	u := fmt.Sprintf("https://releases.hashicorp.com/terraform/%s/terraform_%s_linux_amd64.zip",
		vers, vers)
	err := downloadFile(u, `/tmp/terraform.zip`)
	if err != nil {
		return "", err
	}

	files, err := unzip(`/tmp/terraform.zip`, `/tmp`)
	if err != nil {
		return "", err
	}
	err = os.Remove("/tmp/terraform.zip")
	if err != nil {
		return "", fmt.Errorf("failed to remove /tmp/terraform.zip: %w", err)
	}

	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			return "", fmt.Errorf("failed to stat file %s -> %w", f, err)
		}

		if fi.IsDir() {
			continue
		}

		if filepath.Base(f) == "terraform" {
			return f, nil
		}
	}

	return "", fmt.Errorf("failed to find terraform binary in zip")
}

// using zip implementation from https://golangcode.com/unzip-files-in-go/
// to prevent package dependency
// nolint
func unzip(src string, dest string) ([]string, error) {
	var filenames []string

	r, err := zip.OpenReader(src)
	if err != nil {
		return filenames, err
	}
	defer r.Close()

	for _, f := range r.File {
		// Store filename/path for returning and using later on
		fpath := filepath.Join(dest, f.Name) // #nosec

		// Check for ZipSlip. More Info: http://bit.ly/2MsjAWE
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return filenames, fmt.Errorf("%s: illegal file path", fpath)
		}

		filenames = append(filenames, fpath)

		if f.FileInfo().IsDir() {
			// Make Folder
			err := os.MkdirAll(fpath, os.ModePerm)
			if err != nil {
				return nil, fmt.Errorf("failed to create subdirectory: %w", err)
			}
			continue
		}

		// Make File
		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return filenames, err
		}

		outFile, err := os.OpenFile(filepath.Clean(fpath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return filenames, err
		}

		rc, err := f.Open()
		if err != nil {
			return filenames, err
		}

		for {
			_, err = io.CopyN(outFile, rc, 1024)
			if err != nil {
				if err == io.EOF {
					break
				}
				return filenames, err
			}
		}

		// Close the file without defer to close before next iteration of loop
		err = outFile.Close()
		if err != nil {
			return filenames, fmt.Errorf("failed to close outFile: %v", err)
		}

		err = rc.Close()
		if err != nil {
			return filenames, fmt.Errorf("failed to close zip file: %v", err)
		}
	}
	return filenames, nil
}

func latest() (string, error) {
	u := "https://github.com/hashicorp/terraform/releases/latest"

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Head(u)
	if err != nil {
		return "", fmt.Errorf("failed to HEAD terraform repo: %w", err)
	}
	defer resp.Body.Close()

	location, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("failed to get redirect location for terraform repo: %w", err)
	}

	return strings.TrimLeft(filepath.Base(location.String()), "v"), nil
}

func downloadFile(u string, path string) error {
	resp, err := http.Get(u) // #nosec
	if err != nil {
		return fmt.Errorf("failed to download: %v", err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			log.Printf("failed to close HTTP response body: %v", err)
		}
	}()

	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer func() {
		err := f.Close()
		if err != nil {
			log.Printf("failed to close %s file handle: %v", path, err)
		}
	}()

	_, err = io.Copy(f, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to save: %w", err)
	}
	return nil
}
