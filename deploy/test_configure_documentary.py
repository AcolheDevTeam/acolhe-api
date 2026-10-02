"""Run with: python3 -m unittest discover -s deploy -p 'test_*.py'."""
import importlib.util
import json
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('configure_documentary', Path(__file__).with_name('configure-documentary.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ConfigureDocumentaryTest(unittest.TestCase):
    def test_valid_update_preserves_other_variables_and_permissions(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / '.env'
            path.write_text('OTHER=preserved\nDOCUMENTARY_ACTIVE_KEY_ID=old\n')
            env = {'DOCUMENTARY_ACTIVE_KEY_ID': 'v2', 'DOCUMENTARY_ENCRYPTION_KEYS': json.dumps({'v2': 'ab' * 32})}
            with patch.dict(os.environ, env):
                module.configure(path)
            self.assertIn('OTHER=preserved\n', path.read_text())
            self.assertEqual(path.read_text().count('DOCUMENTARY_ACTIVE_KEY_ID='), 1)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)

    def test_invalid_or_duplicate_keys_do_not_mutate_dotenv(self):
        for raw in ['{}', '{"v1":"invalid"}', '{"v1":"' + 'ab' * 32 + '","v1":"' + 'cd' * 32 + '"}']:
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / '.env'
                path.write_text('OTHER=preserved\n')
                with patch.dict(os.environ, {'DOCUMENTARY_ACTIVE_KEY_ID': 'v1', 'DOCUMENTARY_ENCRYPTION_KEYS': raw}):
                    with self.assertRaises(ValueError):
                        module.configure(path)
                self.assertEqual(path.read_text(), 'OTHER=preserved\n')
