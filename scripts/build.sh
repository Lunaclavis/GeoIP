#!/bin/bash

set -euo pipefail

go build -o genmmdb ../main.go
./genmmdb
