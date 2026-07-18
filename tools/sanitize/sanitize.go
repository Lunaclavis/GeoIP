package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

const maxFiles = 10

var (
	outputDir  string
	inputFiles []string
)

func main() {
	flag.StringVar(&outputDir, "d", "", "Target directory for output")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Usage: %s [-d <dir>] <file1> [<file2> ...]\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}

	flag.Parse()

	if flag.NArg() > maxFiles {
		fmt.Fprintln(os.Stderr, "Error: Too many input files")
		flag.Usage()
		os.Exit(1)
	} else if flag.NArg() > 0 {
		inputFiles = flag.Args()
	} else {
		fmt.Fprintln(os.Stderr, "Error: No input files specified")
		flag.Usage()
		os.Exit(1)
	}

	if outputDir != "" {
		dirExist, err := checkDir(outputDir)
		if err != nil {
			log.Fatalf("Error: %v\n\n", err)
		}
		if !dirExist {
			if err := os.MkdirAll(outputDir, 0o777); err != nil {
				log.Fatalf("Error: Failed to create output directory %q\n%v\n\n", outputDir, err)
			}
		}
	}

	for _, inputFile := range inputFiles {
		txtBuilder, err := cleanFile(inputFile)
		if err != nil {
			log.Fatalf("Error: %v\n\n", err)
		}

		var overwrite bool
		var outputFile string
		if outputDir != "" {
			outputFile = filepath.Join(outputDir, filepath.Base(inputFile))
		} else {
			outputFile = inputFile
			overwrite = true
		}

		if err := writeOut(outputFile, txtBuilder); err != nil {
			log.Fatalf("Error: %v\n\n", err)
		}

		if overwrite {
			log.Printf("Info: Completely sanitized and overwritten file %q\n", outputFile)
		} else {
			log.Printf("Info: Completely sanitized file %q and saved to: %q\n", inputFile, outputFile)
		}
	}

	if flag.NArg() > 1 {
		log.Printf("Info: All files sanitized successfully\n")
	}
	fmt.Fprintln(os.Stderr)
}

func checkDir(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return true, nil
	}
	return false, fmt.Errorf("%q is not a valid directory", dir)
}

func cleanFile(path string) (*strings.Builder, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read file %q\n%w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory", path)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("File %q is empty", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open file %q\n%w", path, err)
	}
	defer file.Close()

	var builder strings.Builder
	builder.Grow(int(info.Size()))

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		line = strings.Map(func(r rune) rune {
			if r == ',' || r == ';' {
				return ' '
			}
			if (r >= '0' && r <= '9') ||
				(r >= 'a' && r <= 'f') ||
				(r >= 'A' && r <= 'F') ||
				r == '.' || r == ':' || r == '/' || r == ' ' {
				return r
			}
			return -1
		}, line)

		for field := range strings.FieldsSeq(line) {
			prefix, err := checkStr(field)
			if err != nil {
				log.Printf("Warning: %v\n", err)
				continue
			}
			builder.WriteString(prefix)
			builder.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("Failed to read file %q\n%w", path, err)
	}

	if builder.Len() == 0 {
		return nil, fmt.Errorf("No valid content found in file %q", path)
	}

	return &builder, nil
}

func checkStr(field string) (string, error) {
	prefix, err := netip.ParsePrefix(field)
	if err != nil {
		return "", fmt.Errorf("Invalid CIDR format %q removed\n%w", field, err)
	}

	maskedPrefix := prefix.Masked()
	return maskedPrefix.String(), nil
}

func writeOut(path string, builder *strings.Builder) (wErr error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		cErr := file.Close()
		if cErr != nil && wErr == nil {
			wErr = cErr
		}
	}()

	_, err = file.WriteString(builder.String())
	return err
}
