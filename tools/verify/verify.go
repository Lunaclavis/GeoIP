package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/oschwald/maxminddb-golang/v2"
)

const (
	maxFileIPs = 30
	maxArgsIPs = 20
)

var (
	infoPtr     *bool
	singlePtr   *bool
	MMDBPathPtr *string
	filePathPtr *string

	warnFlag bool
)

func main() {
	log.SetFlags(0)

	infoPtr = flag.Bool("i", false, "Display MMDB database information")
	singlePtr = flag.Bool("s", false, "Single mode")
	MMDBPathPtr = flag.String("d", "Country.mmdb", "Path to the MMDB database file")
	filePathPtr = flag.String("f", "", "Path to the file containing IP addresses (one per line)")

	flag.Usage = func() {
		base := filepath.Base(os.Args[0])
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Usage:\nNormal mode: %s [-i] [-d <MMDB>] [-f <file>] [<IP1> <IP2> ...]\nSingle mode: %s [-i] [-d <MMDB>] -s <IP>\n", base, base)
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}

	flag.Parse()

	if err := verify(); err != nil {
		log.Fatalf("Error: %v\n\n", err)
	}
}

func verify() error {
	mmdb, err := maxminddb.Open(*MMDBPathPtr)
	if err != nil {
		return err
	}
	defer mmdb.Close()

	if *singlePtr {
		if *filePathPtr != "" {
			return errors.New("input files are not supported in single mode")
		}
		if flag.NArg() < 1 {
			return errors.New("no input IP address specified")
		}
		if flag.NArg() > 1 {
			return errors.New("only one IP address is allowed in single mode")
		}
		if err := checkMMDB(mmdb); err != nil {
			return fmt.Errorf("database %s validation failed: %w", *MMDBPathPtr, err)
		}
		return singleMode(mmdb)
	}

	var list []netip.Addr
	if *filePathPtr != "" || flag.NArg() > 0 {
		list = make([]netip.Addr, 0, maxFileIPs+maxArgsIPs)
		if err := checkFile(*filePathPtr, &list); err != nil {
			return err
		}
		checkArgs(&list)
		if len(list) == 0 {
			return errors.New("no valid IP address found")
		}
	}

	if err := checkMMDB(mmdb); err != nil {
		return fmt.Errorf("database %s validation failed: %w", *MMDBPathPtr, err)
	}

	return lookupMMDB(mmdb, list)
}

func checkMMDB(mmdb *maxminddb.Reader) error {
	if err := mmdb.Verify(); err != nil {
		return err
	}

	if *infoPtr {
		metadata := mmdb.Metadata
		fmt.Println()
		fmt.Printf("Build Time: %s\n", metadata.BuildTime().Format("2006-01-02 15:04:05 -07:00"))

		info, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(info))
		fmt.Println()
	}

	return nil
}

func checkFile(path string, listPtr *[]netip.Addr) error {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)

outer:
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
			if r == ',' || r == ';' || r == '%' || unicode.IsSpace(r) {
				return ' '
			}
			if (r >= '0' && r <= '9') ||
				(r >= 'a' && r <= 'f') ||
				(r >= 'A' && r <= 'F') ||
				r == '.' || r == ':' {
				return r
			}
			return '*'
		}, line)

		for field := range strings.FieldsSeq(line) {
			addr, err := netip.ParseAddr(field)
			if err != nil {
				log.Printf("Warning: skip invalid IP address format: %v\n", err)
				warnFlag = true
				continue
			}

			if count >= maxFileIPs {
				log.Printf("Warning: file %s contains more than %v IP addresses, ignoring the rest\n", path, maxFileIPs)
				warnFlag = true
				break outer
			}

			*listPtr = append(*listPtr, addr)
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func checkArgs(listPtr *[]netip.Addr) {
	count := 0
	for _, arg := range flag.Args() {
		addr, err := netip.ParseAddr(arg)
		if err != nil {
			log.Printf("Warning: skip invalid IP address format: %v\n", err)
			warnFlag = true
			continue
		}

		if count >= maxArgsIPs {
			log.Printf("Warning: command line contains more than %v IP addresses, ignoring the rest\n", maxArgsIPs)
			warnFlag = true
			break
		}

		*listPtr = append(*listPtr, addr)
		count++
	}
}

func lookupMMDB(mmdb *maxminddb.Reader, list []netip.Addr) error {
	if len(list) == 0 {
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "IP Address\tGeoCode")
	fmt.Fprintln(w, "---------------\t---------")

	for _, addr := range list {
		result := mmdb.Lookup(addr)
		if err := result.Err(); err != nil {
			return err
		}

		var code string
		if !result.Found() {
			code = "UNKNOWN"
		} else {
			if err := result.DecodePath(&code, "country", "iso_code"); err != nil {
				return err
			}
			if code == "" {
				code = "UNKNOWN"
			}
		}

		fmt.Fprintf(w, "%s\t%s\n", addr, code)
	}

	if warnFlag {
		fmt.Fprintln(os.Stderr)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Println()

	return nil
}

func singleMode(mmdb *maxminddb.Reader) error {
	arg := flag.Arg(0)
	addr, err := netip.ParseAddr(arg)
	if err != nil {
		return fmt.Errorf("invalid IP address format: %w", err)
	}

	result := mmdb.Lookup(addr)
	if err := result.Err(); err != nil {
		return err
	}
	if !result.Found() {
		return fmt.Errorf("no record found for IP address %q", arg)
	}

	var record any
	if err := result.Decode(&record); err != nil {
		return err
	}

	info, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(info))
	fmt.Println()

	return nil
}
