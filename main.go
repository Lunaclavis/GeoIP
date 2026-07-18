package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

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
	var (
		stream  bool
		srcFile string
		outFile string
	)

	flag.BoolVar(&stream, "s", false, "Use standard streaming (line-by-line) instead of loading into memory")
	flag.StringVar(&srcFile, "f", "Mainland.txt", "Source CIDR file path")
	flag.StringVar(&outFile, "o", "Country.mmdb", "Output MMDB file path")
	flag.Parse()

	// Create a new MMDB tree
	writer, err := mmdbwriter.New(
		mmdbwriter.Options{
			DatabaseType:            "GeoIP2-Country",
			Description:             map[string]string{"en": "Custom GeoIP2 Database"},
			Languages:               []string{"de", "en", "es", "fr", "ja", "pt-BR", "ru", "zh-CN"},
			RecordSize:              24,
			IncludeReservedNetworks: true,
		},
	)
	if err != nil {
		log.Fatalf("Error: Failed to create MMDB tree\n%v\n", err)
	}

	// Populate the database
	if err := populate(srcFile, writer, dataCN, stream); err != nil {
		log.Fatalf("Error: %v\n", err)
	}

	// Write the database to disk
	if err := writeOut(outFile, writer); err != nil {
		log.Fatalf("Error: %v\n", err)
	}

	// Print completion message
	log.Println("Info: Successfully generated the GeoIP2 Database")
}

func populate(path string, tree *mmdbwriter.Tree, data mmdbtype.DataType, stream bool) error {
	var reader io.Reader

	// Read source file
	if stream {
		log.Println("Info: Reading source file line by line via stream...")
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("Failed to open file %q\n%w", path, err)
		}
		defer file.Close()
		reader = file
	} else {
		log.Println("Info: Loading entire source file into memory...")
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("Failed to read file %q into memory\n%w", path, err)
		}
		reader = bytes.NewReader(content)
	}

	// Scan the file
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse String
		_, network, err := net.ParseCIDR(line)
		if err != nil || network == nil {
			log.Printf("Warning: Invalid CIDR format %q skipped\n%v\n", line, err)
			continue
		}

		// Insert to the database
		if err := tree.Insert(network, data); err != nil {
			return fmt.Errorf("Failed to insert %q to tree\n%w", network.String(), err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("Failed to scan file %q\n%w", path, err)
	}

	return nil
}

func writeOut(path string, writer *mmdbwriter.Tree) (writeErr error) {
	// Create output file
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Failed to create output file %q\n%w", path, err)
	}
	defer func() {
		closeErr := file.Close()
		if closeErr != nil && writeErr == nil {
			writeErr = fmt.Errorf("Failed to close output file %q\n%w", path, closeErr)
		}
	}()

	// Write to the output file
	if _, err := writer.WriteTo(file); err != nil {
		return fmt.Errorf("Failed to write to output file %q\n%w", path, err)
	}

	return nil
}
