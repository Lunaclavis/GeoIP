package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/maxmind/mmdbwriter/v2"
	"github.com/maxmind/mmdbwriter/v2/mmdbtype"
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

	flag.BoolVar(&stream, "s", false, "Stream the file line by line rather than loading it into memory")
	flag.StringVar(&srcFile, "f", "Mainland.txt", "Source CIDR list file path")
	flag.StringVar(&outFile, "o", "Country.mmdb", "Output MMDB database file path")
	flag.Parse()

	var err error
	outFile, err = filepath.Abs(outFile)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}

	isExist, err := checkPath(outFile)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
	if isExist {
		log.Fatalf("Error: Path %q already exists\n", outFile)
	}

	outDirNoDesc := filepath.Join(filepath.Dir(outFile), "NoDesc")
	outFileNoDesc := filepath.Join(outDirNoDesc, filepath.Base(outFile))
	if err := os.Mkdir(outDirNoDesc, 0o777); err != nil {
		log.Fatalf("Error: Failed to create output directory %q\n%v\n", outDirNoDesc, err)
	}

	isExist, err = checkPath(outFileNoDesc)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
	if isExist {
		log.Fatalf("Error: Path %q already exists\n", outFileNoDesc)
	}

	// Create a new MMDB tree
	mmdbTree, err := mmdbwriter.New(
		mmdbwriter.Options{
			DatabaseType:            "GeoLite2-Country",
			Description:             map[string]string{"en": "Custom GeoLite2 Country Database"},
			Languages:               []string{"de", "en", "es", "fr", "ja", "pt-BR", "ru", "zh-CN"},
			RecordSize:              24,
			IncludeReservedNetworks: true,
		},
	)
	if err != nil {
		log.Fatalf("Error: Failed to create MMDB tree\n%v\n", err)
	}

	mmdbTreeNoDesc, err := mmdbwriter.New(
		mmdbwriter.Options{
			DatabaseType:            "GeoLite2-Country",
			RecordSize:              24,
			IncludeReservedNetworks: true,
		},
	)
	if err != nil {
		log.Fatalf("Error: Failed to create MMDB tree without Description\n%v\n", err)
	}

	mmdbTrees := []*mmdbwriter.Tree{mmdbTree, mmdbTreeNoDesc}

	// Populate the database
	if err := populate(srcFile, mmdbTrees, dataCN, stream); err != nil {
		log.Fatalf("Error: %v\n", err)
	}

	// Write the database to disk
	if err := writeOut(outFile, mmdbTree); err != nil {
		log.Fatalf("Error: %v\n", err)
	}

	if err := writeOut(outFileNoDesc, mmdbTreeNoDesc); err != nil {
		log.Fatalf("Error: %v\n", err)
	}

	// Print completion message
	log.Println("Info: Successfully generated the GeoIP2 Database")
}

func checkPath(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return true, fmt.Errorf("Path %q is a directory", path)
	}
	return true, nil
}

func populate(path string, mmdbTrees []*mmdbwriter.Tree, data mmdbtype.DataType, stream bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("Failed to read file %q\n%w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("Path %q is a directory", path)
	}
	if info.Size() < 1 {
		return fmt.Errorf("File %q is empty", path)
	}
	if info.Size() > 3<<20 {
		return fmt.Errorf("File %q is too large", path)
	}

	// Read source file
	var reader io.Reader
	if !stream && info.Size() < 3<<20 {
		log.Println("Info: Loading entire source file into memory...")
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("Failed to read file %q\n%w", path, err)
		}
		reader = bytes.NewReader(content)
	} else {
		log.Println("Info: Reading source file line by line via stream...")
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("Failed to read file %q\n%w", path, err)
		}
		defer file.Close()
		reader = file
	}

	// Scan the file
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse String
		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			log.Printf("Warning: Invalid CIDR format %q skipped\n%v\n", line, err)
			continue
		}

		maskedPrefix := prefix.Masked()
		if !maskedPrefix.IsValid() {
			log.Printf("Warning: Invalid CIDR format %q skipped\n%v\n", line, err)
			continue
		}

		// Insert to all databases
		for _, mmdbTree := range mmdbTrees {
			if err := mmdbTree.Insert(maskedPrefix, data); err != nil {
				return fmt.Errorf("Failed to insert %q to all trees\n%w", prefix.String(), err)
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return fmt.Errorf("Failed to scan file %q\n%w", path, err)
	}

	return nil
}

func writeOut(path string, mmdbTree *mmdbwriter.Tree) (errWrite error) {
	// Create output file
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Failed to create output file %q\n%w", path, err)
	}

	defer func() {
		errClose := file.Close()
		if errClose != nil && errWrite == nil {
			errWrite = fmt.Errorf("Failed to close output file %q\n%w", path, errClose)
		}
	}()

	// Write to the output file
	if _, err := mmdbTree.WriteTo(file); err != nil {
		return fmt.Errorf("Failed to write to output file %q\n%w", path, err)
	}

	return nil
}
