package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/maxmind/mmdbwriter/v2"
	"github.com/maxmind/mmdbwriter/v2/mmdbtype"
)

const maxFileSize = 2 << 20

var (
	dataCN = mmdbtype.Map{
		"country": mmdbtype.Map{
			"geoname_id": mmdbtype.Uint32(1814991),
			"iso_code":   mmdbtype.String("CN"),
			"names": mmdbtype.Map{
				"de":    mmdbtype.String("China"),
				"en":    mmdbtype.String("China"),
				"es":    mmdbtype.String("China"),
				"fr":    mmdbtype.String("Chine"),
				"ja":    mmdbtype.String("中国"),
				"pt-BR": mmdbtype.String("China"),
				"ru":    mmdbtype.String("Китай"),
				"zh-CN": mmdbtype.String("中国"),
			},
		},
	}
)

func main() {
	var srcFile string
	var dstFile string

	flag.StringVar(&srcFile, "s", "Mainland.txt", "Source CIDR list file path")
	flag.StringVar(&dstFile, "d", "Country.mmdb", "Target MMDB database file path")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Usage: %s [-s <SrcPath>] [-d <DstPath>]\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}

	flag.Parse()

	if flag.NArg() != 0 {
		log.Fatalf("Error: unexpected arguments: %v\n", flag.Args())
	}

	if err := generate(srcFile, dstFile); err != nil {
		log.Fatalf("Error: %v\n", err)
	}

	log.Println("Info: successfully generated the GeoIP2 Database")
}

func generate(srcFile, dstFile string) error {
	srcFile, err := filepath.Abs(srcFile)
	if err != nil {
		return err
	}

	root, dstFiles, err := ensurePaths(dstFile)
	if err != nil {
		return err
	}
	defer root.Close()

	mmdbTree, err := mmdbwriter.New(
		mmdbwriter.Options{
			DatabaseType:            "GeoLite2-Country",
			Description:             map[string]string{"en": "Custom GeoLite2 Country Database"},
			Languages:               []string{"de", "en", "es", "fr", "ja", "pt-BR", "ru", "zh-CN"},
			RecordSize:              24,
			IncludeReservedNetworks: false,
		},
	)
	if err != nil {
		return fmt.Errorf("create tree: %w\n", err)
	}

	mmdbTreeNoDesc, err := mmdbwriter.New(
		mmdbwriter.Options{
			DatabaseType:            "GeoLite2-Country",
			RecordSize:              24,
			IncludeReservedNetworks: false,
		},
	)
	if err != nil {
		return fmt.Errorf("create tree: %w\n", err)
	}

	mmdbTrees := []*mmdbwriter.Tree{mmdbTree, mmdbTreeNoDesc}

	if err := populate(srcFile, mmdbTrees, dataCN); err != nil {
		return err
	}

	for i := range dstFiles {
		if err := writeOut(root, dstFiles[i], mmdbTrees[i]); err != nil {
			return err
		}
	}

	return nil
}

func checkFile(root *os.Root, path string) error {
	info, err := root.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path is a symbolic link: %s", path)
		}
		return fmt.Errorf("path already exists: %s", path)
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return err
	}
}

func ensurePaths(path string) (*os.Root, []string, error) {
	dstFile, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	dir, base := filepath.Split(dstFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	if err := checkFile(root, base); err != nil {
		root.Close()
		return nil, nil, err
	}
	var baseNoDesc string
	if i := strings.LastIndex(base, "."); i != -1 && i != 0 {
		baseNoDesc = base[:i] + "NoDesc" + base[i:]
	} else {
		baseNoDesc = base + "NoDesc"
	}
	if err := checkFile(root, baseNoDesc); err != nil {
		root.Close()
		return nil, nil, err
	}
	return root, []string{base, baseNoDesc}, nil
}

func readFile(path string) ([]byte, error) {
	statInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if !statInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file: %s", path)
	}
	fileSize := statInfo.Size()
	if fileSize < 1 {
		return nil, fmt.Errorf("file is empty: %s", path)
	}
	if fileSize > maxFileSize {
		return nil, fmt.Errorf("file is too large: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() || !os.SameFile(statInfo, fileInfo) {
		return nil, fmt.Errorf("file was replaced while being opened: %s", path)
	}
	if fileInfo.Size() != fileSize {
		return nil, fmt.Errorf("file was modified while being read: %s", path)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if len(data) != int(fileSize) {
		return nil, fmt.Errorf("file was modified while being read: %s", path)
	}

	return data, nil
}

func populate(path string, mmdbTrees []*mmdbwriter.Tree, data mmdbtype.DataType) error {
	// Read source file
	content, err := readFile(path)
	if err != nil {
		return err
	}

	// Scan the content
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse String
		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			log.Printf("Warning: skip invalid CIDR format: %v\n", err)
			continue
		}
		maskedPrefix := prefix.Masked()

		// Insert to all databases
		for _, mmdbTree := range mmdbTrees {
			if err := mmdbTree.Insert(maskedPrefix, data); err != nil {
				return fmt.Errorf("failed to insert %q into tree\n%w", prefix.String(), err)
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return fmt.Errorf("scan file %s: %w", path, err)
	}

	return nil
}

func writeOut(root *os.Root, path string, mmdbTree *mmdbwriter.Tree) (err error) {
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	defer func() {
		closeErr := file.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("close file: %w", closeErr)
		}
		if err != nil {
			if removeErr := root.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				log.Printf("Warning: failed to remove incomplete output file: %v", removeErr)
			}
		}
	}()

	if _, err := mmdbTree.WriteTo(file); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}
