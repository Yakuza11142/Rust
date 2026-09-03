#!/bin/bash
set -e

echo "=========================================================="
echo "🧪 Running System Latency and Performance Audits..."
echo "=========================================================="

# Invoke inline Python script to execute the stealth pipeline
python3 -c "
import time
from wrappers.python.client import PlatformEngineClient

try:
    print('[+] Initializing stealth core network client bindings...')
    client = PlatformEngineClient()
    
    start_time = time.time()
    print('[+] Routing handshake profile to test target (httpbin)...')
    response_data = client.execute_pipeline('https://httpbin.org', runtime_timeout=15)
    execution_duration = time.time() - start_time
    
    print(f'✅ BENCHMARK PASSED: Handshake finalized in {execution_duration:.4f} seconds.')
    print(f'[+] Extracted payload snapshot size: {len(response_data)} bytes.')
except Exception as error:
    print(f'❌ BENCHMARK FAILED: Operational error triggered: {error}')
"
echo "=========================================================="
