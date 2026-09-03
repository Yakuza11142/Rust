#!/bin/bash
set -e

echo "=========================================================="
echo "🚀 Initializing Local Target Cross-Compilation Pipeline..."
echo "=========================================================="

# Enforce strict 64-bit Linux target variables regardless of the host OS
export GOOS=linux
export GOARCH=amd64

# Execute the c-shared build command directly
go build -buildmode=c-shared -o wrappers/python/libscraper.so engine.go

echo "✅ SUCCESS: Exported dynamically linked x86_64 .so binary into Python module layer."
echo "=========================================================="
