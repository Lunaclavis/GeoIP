#!/bin/bash

set -euo pipefail

go build -o sanitize ../tools/sanitize/sanitize.go


INPUT=(
    "Mainland-IPv4 ipv4_addr"
    "Mainland-IPv6 ipv6_addr"
)

convert_to_nft_set() {
    local input_file="$1"
    local output_file="$2"
    local ip_type="$3"
    local set_name="$4"

    awk -v ip_type="$ip_type" -v set_name="$set_name" '
        BEGIN {
            print "set " set_name " {"
            print "    type " ip_type
            print "    flags interval"
            print "    elements = {"
        }
        {
            if (NR > 1) {
                print ","
            }
            printf "        %s", $0
        }
        END {
            printf "\n    }\n}\n"
        }
    ' "$input_file" > "$output_file" || {
        echo "Error: awk failed for file '$input_file'" >&2
        exit 1
    }
}

for row in "${INPUT[@]}"; do
    read -r name type <<< "$row"
    ./sanitize "${name}.txt"
    convert_to_nft_set "${name}.txt" "${name}.set" "${type}" "${name}"
done

cat Mainland-IPv4.txt Mainland-IPv6.txt > Mainland.txt

echo "Info: all conversions completed successfully"
