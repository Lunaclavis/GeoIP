#!/bin/bash

set -euo pipefail

go build -o verify ../tools/verify/verify.go
./verify 1.1.1.1 119.29.29.29 2606:4700:4700::1111 2402:4e00::
