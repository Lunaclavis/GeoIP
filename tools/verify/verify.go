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

	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/oschwald/maxminddb-golang/v2/mmdbdata"
)

var (
	infoPtr     *bool
	singlePtr   *bool
	MMDBPathPtr *string
	filePathPtr *string

	warnFlag bool
	dataFlag bool
)

type ISOCode string

func (c *ISOCode) UnmarshalMaxMindDBCursor(cursor mmdbdata.Cursor) (mmdbdata.Cursor, error) {
	entries, err := cursor.Map()
	if err != nil {
		return mmdbdata.Cursor{}, mmdbdata.NormalizeUnmarshalError[ISOCode](err)
	}
	var next mmdbdata.Cursor
	for {
		key, valueCursor, ok := entries.Next(next)
		if !ok {
			break
		}
		switch string(key) {
		case "country":
			next, err = c.unmarshalCountry(valueCursor)
		default:
			next, err = valueCursor.Skip()
		}
		if err != nil {
			return mmdbdata.Cursor{}, err
		}
	}
	return entries.End()
}

func (c *ISOCode) unmarshalCountry(cursor mmdbdata.Cursor) (mmdbdata.Cursor, error) {
	entries, err := cursor.Map()
	if err != nil {
		return mmdbdata.Cursor{}, mmdbdata.NormalizeUnmarshalError[ISOCode](err)
	}
	var next mmdbdata.Cursor
	for {
		key, valueCursor, ok := entries.Next(next)
		if !ok {
			break
		}
		switch string(key) {
		case "iso_code":
			var code string
			code, next, err = valueCursor.ReadString()
			if err != nil {
				return mmdbdata.Cursor{}, mmdbdata.NormalizeUnmarshalError[ISOCode](err)
			}
			*c = ISOCode(code)
		default:
			next, err = valueCursor.Skip()
			if err != nil {
				return mmdbdata.Cursor{}, err
			}
		}
	}
	return entries.End()
}

var _ mmdbdata.CursorUnmarshaler = (*ISOCode)(nil)

func main() {
	infoPtr = flag.Bool("i", false, "Display MMDB database information")
	singlePtr = flag.Bool("s", false, "Single mode")
	MMDBPathPtr = flag.String("p", "Country.mmdb", "Path to the MMDB database file")
	filePathPtr = flag.String("f", "", "Path to the file containing IP addresses (one per line)")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Usage:\nNormal mode: %s [-i] [-d <MMDB>] [-f <file>] [<IP1> <IP2> ...]\nSingle mode: %s [-i] [-d <MMDB>] -s <IP>\n", filepath.Base(os.Args[0]), filepath.Base(os.Args[0]))
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

	if err := checkMMDB(mmdb); err != nil {
		return fmt.Errorf("Database %q validation failed\n%w", *MMDBPathPtr, err)
	}

	if *singlePtr {
		if *filePathPtr != "" {
			return errors.New("Input files are not supported in single mode")
		}
		if flag.NArg() != 1 {
			return errors.New("Only one IP address is allowed in single mode")
		}
		if err := singleMode(mmdb, flag.Arg(0)); err != nil {
			return err
		}
		return nil
	}

	maxFileLen := 30
	maxArgsLen := 20
	ipList := make([]netip.Addr, 0, maxFileLen+maxArgsLen)

	if *filePathPtr != "" {
		if err := checkFile(*filePathPtr, &ipList, maxFileLen); err != nil {
			return err
		}
	}

	if flag.NArg() > 0 {
		checkArgs(&ipList, maxArgsLen)
	}

	if len(ipList) == 0 {
		return errors.New("No valid IP address found")
	}

	if err := lookupMMDB(mmdb, ipList); err != nil {
		return err
	}

	return nil
}

func checkMMDB(mmdb *maxminddb.Reader) error {
	if err := mmdb.Verify(); err != nil {
		return err
	}

	if *infoPtr {
		metadata := mmdb.Metadata
		fmt.Printf("Build Time: %s\n", metadata.BuildTime().Format("2006-01-02 15:04:05 -07:00"))
		fmt.Println()

		info, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(info))
		fmt.Println()
	}

	return nil
}

func checkFile(path string, listPtr *[]netip.Addr, maxLen int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

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
			if r == ',' || r == ';' {
				return ' '
			}
			if (r >= '0' && r <= '9') ||
				(r >= 'a' && r <= 'f') ||
				(r >= 'A' && r <= 'F') ||
				r == '.' || r == ':' || r == ' ' {
				return r
			}
			return -1
		}, line)

		for field := range strings.FieldsSeq(line) {
			ipAddr, err := netip.ParseAddr(field)
			if err != nil || !ipAddr.IsValid() {
				log.Printf("Warning: Invalid IP address format %q skipped\n%v\n", field, err)
				warnFlag = true
				continue
			}

			if len(*listPtr) >= maxLen {
				log.Printf("Warning: File %q contains more than %v IP addresses, ignoring the rest\n", path, maxLen)
				warnFlag = true
				break outer
			}

			*listPtr = append(*listPtr, ipAddr)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func checkArgs(listPtr *[]netip.Addr, maxLen int) {
	ipArgs := flag.Args()
	count := 0
	for _, arg := range ipArgs {
		ipAddr, err := netip.ParseAddr(arg)
		if err != nil || !ipAddr.IsValid() {
			log.Printf("Warning: Invalid IP address format %q skipped\n%v\n", arg, err)
			warnFlag = true
			continue
		}

		if count >= maxLen {
			log.Printf("Warning: Command line contains more than %v IP addresses, ignoring the rest\n", maxLen)
			warnFlag = true
			break
		}

		*listPtr = append(*listPtr, ipAddr)
		count++
	}
}

func lookupMMDB(mmdb *maxminddb.Reader, ipList []netip.Addr) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)

	for _, ipAddr := range ipList {
		result := mmdb.Lookup(ipAddr)
		if err := result.Err(); err != nil {
			return err
		}

		var code ISOCode
		if !result.Found() {
			code = "UNKNOWN"
		} else {
			if err := result.Decode(&code); err != nil {
				return err
			}
		}

		if !dataFlag {
			fmt.Fprintln(w, "IP Address\tGeoCode")
			fmt.Fprintln(w, "---------------\t---------")
			dataFlag = true
		}

		fmt.Fprintf(w, "%s\t%s\n", ipAddr, code)
	}

	if warnFlag {
		fmt.Fprintln(os.Stderr)
	}

	w.Flush()
	fmt.Println()

	return nil
}

func singleMode(mmdb *maxminddb.Reader, str string) error {
	ipAddr, err := netip.ParseAddr(str)
	if err != nil || !ipAddr.IsValid() {
		return fmt.Errorf("Invalid IP address format %q\n%w", str, err)
	}

	result := mmdb.Lookup(ipAddr)
	if err := result.Err(); err != nil {
		return err
	}
	if !result.Found() {
		return fmt.Errorf("No record found for IP address %q", str)
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
