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
)

const (
	maxFileSize = 2 << 20
	maxLineSize = 1 << 20
)

func main() {
	var outputDir string

	log.SetFlags(0)

	flag.StringVar(&outputDir, "d", "", "Target directory for output")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Usage: %s [-d <dir>] <file>\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}

	flag.Parse()

	if flag.NArg() != 1 {
		if flag.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "Error: no input file specified")
		} else {
			fmt.Fprintln(os.Stderr, "Error: only one file is supported")
		}
		flag.Usage()
		os.Exit(1)
	}

	inputFile, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatalf("Error: %v\n\n", err)
	}

	if err := sanitize(inputFile, outputDir); err != nil {
		log.Fatalf("Error: %v\n\n", err)
	}
}

func sanitize(inputFile, outputDir string) error {
	dir, base := filepath.Split(inputFile)
	if outputDir == "" {
		outputDir = dir
	}
	root, outputDir, err := ensureDir(outputDir)
	if err != nil {
		return err
	}
	defer root.Close()

	content, err := cleanFile(inputFile)
	if err != nil {
		return err
	}

	if err := writeOut(root, base, content); err != nil {
		return err
	}

	outputFile := filepath.Join(outputDir, base)
	if inputFile == outputFile {
		log.Printf("Info: completely sanitized and overwritten file %s\n", inputFile)
	} else {
		log.Printf("Info: completely sanitized file %s and saved to %s\n", inputFile, outputFile)
	}

	return nil
}

func ensureDir(path string) (*os.Root, string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	lstatInfo, err := os.Lstat(path)
	switch {
	case err == nil:
		if lstatInfo.Mode()&os.ModeSymlink != 0 {
			return nil, "", fmt.Errorf("path is a symbolic link: %s", path)
		}
		if !lstatInfo.IsDir() {
			return nil, "", fmt.Errorf("path exists but is not a valid directory: %s", path)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, "", fmt.Errorf("create directory: %s: %w", path, err)
		}
		lstatInfo, err = os.Lstat(path)
		if err != nil {
			return nil, "", err
		}
		if !lstatInfo.IsDir() {
			return nil, "", fmt.Errorf("path was replaced during directory creation: %s", path)
		}
	default:
		return nil, "", err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, "", err
	}
	rootInfo, err := root.Lstat(".")
	if err != nil {
		root.Close()
		return nil, "", err
	}
	if !rootInfo.IsDir() || !os.SameFile(lstatInfo, rootInfo) {
		root.Close()
		return nil, "", fmt.Errorf("path was replaced during directory creation: %s", path)
	}
	return root, path, nil
}

func readFile(path string) ([]byte, error) {
	lstatInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if lstatInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("path is a symbolic link: %s", path)
	}
	if !lstatInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file: %s", path)
	}
	fileSize := lstatInfo.Size()
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
	if !fileInfo.Mode().IsRegular() || !os.SameFile(lstatInfo, fileInfo) {
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

func cleanFile(path string) ([]byte, error) {
	content, err := readFile(path)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Grow(len(content))

	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 64<<10), maxLineSize)

	replacer := strings.NewReplacer(",", " ", ";", " ")
	seen := make(map[netip.Prefix]struct{})

	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\uFEFF")
			first = false
		}
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}

		for field := range strings.FieldsSeq(line) {
			field = replacer.Replace(field)

			for s := range strings.FieldsSeq(field) {
				prefix, err := netip.ParsePrefix(s)
				if err != nil {
					continue
				}
				p := prefix.Masked()
				if _, ok := seen[p]; ok {
					continue
				}
				seen[p] = struct{}{}
				buf.WriteString(p.String())
				buf.WriteByte('\n')
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan file %s: %w", path, err)
	}

	if len(seen) == 0 {
		return nil, fmt.Errorf("no valid content found in file: %s", path)
	}

	return buf.Bytes(), nil
}

func writeOut(root *os.Root, path string, data []byte) (err error) {
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}

	defer func() {
		closeErr := file.Close()
		if closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	_, err = file.Write(data)
	return
}
