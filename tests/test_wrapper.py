import unittest
import sys
import os

# Append project root to path to ensure clean environment resolution
sys.path.append(os.path.abspath(os.path.join(os.path.dirname(__file__), '..')))
from wrappers.python.client import PlatformEngineClient

class TestPlatformEngineBindings(unittest.TestCase):
    def test_invalid_url_schema_validation(self):
        """Verify that the Python client layer halts non-web schema transfers early."""
        client = PlatformEngineClient()
        with self.assertRaises(ValueError):
            client.execute_pipeline("ftp://invalid-target-protocol-schema.com")

if __name__ == '__main__':
    unittest.main()
