import ctypes
import json
import os
from typing import Dict, Any
from .exceptions import EngineException

class PlatformEngineClient:
    def __init__(self, binary_directory: str = None):
        resolved_dir = binary_directory or os.path.dirname(os.path.abspath(__file__))
        self.binary_path = os.path.join(resolved_dir, "libscraper.so")
        
        if not os.path.exists(self.binary_path):
            # Fallback scanner path configuration to support nested local development
            self.binary_path = os.path.abspath(os.path.join(resolved_dir, "../../libscraper.so"))
            if not os.path.exists(self.binary_path):
                raise FileNotFoundError(f"CRITICAL: Compiled shared binary missing from designated target paths: {self.binary_path}")
        
        # Load the dynamic Linux Shared Object library via ctypes
        self._engine = ctypes.CDLL(self.binary_path)
        
        # Explicitly configure strict typing signatures across the foreign functional boundary
        self._engine.ExecuteStatelessScrape.argtypes = [ctypes.c_char_p]
        self._engine.ExecuteStatelessScrape.restype = ctypes.c_char_p
        
        self._engine.ReleaseCMemory.argtypes = [ctypes.c_char_p]
        self._engine.ReleaseCMemory.restype = None

    def execute_pipeline(self, routing_url: str, runtime_timeout: int = 15, specialized_headers: Dict[str, str] = None) -> str:
        if not routing_url.startswith(("http://", "https://")):
            raise ValueError("VALIDATION_ERROR: Target parameters must utilize valid network protocol anchors.")

        active_headers = specialized_headers or {}
        
        # Construct dynamic orchestration frame mapping to the Go engine structure
        orchestration_package = {
            "target_url": routing_url,
            "timeout_seconds": runtime_timeout,
            "request_headers": active_headers
        }
        
        # Serialize to dynamic JSON bytes for safe memory-space transfer
        serialized_bytes = json.dumps(orchestration_package).encode('utf-8')
        raw_c_output_pointer = self._engine.ExecuteStatelessScrape(serialized_bytes)
        
        try:
            parsed_response_string = raw_c_output_pointer.decode('utf-8')
            
            # Intercept protocol errors immediately before serving the client application
            if parsed_response_string.startswith(("SYSTEM_ERROR", "EXECUTION_FAILURE", "STREAM_ERROR")):
                raise EngineException(parsed_response_string)
                
            return parsed_response_string
        finally:
            # Deterministic heap deallocation hook to keep background processes stable under high load
            self._engine.ReleaseCMemory(raw_c_output_pointer)
