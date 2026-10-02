#!/usr/bin/env python3
"""Validate documentary secrets and atomically update the protected dotenv file."""
import json
import os
from pathlib import Path
import re
import sys
import tempfile


def configure(path: Path) -> None:
    active = os.environ.get('DOCUMENTARY_ACTIVE_KEY_ID', '')
    raw = os.environ.get('DOCUMENTARY_ENCRYPTION_KEYS', '')
    if not active and not raw:
        return  # Existing installations keep their configured keyring.

    def unique_object(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('duplicate identifier')
            result[key] = value
        return result

    keys = json.loads(raw, object_pairs_hook=unique_object)
    if not isinstance(keys, dict) or active not in keys:
        raise ValueError('invalid active identifier')
    for key, value in keys.items():
        if not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', key) or not isinstance(value, str) or not re.fullmatch(r'[0-9a-fA-F]{64}', value):
            raise ValueError('invalid keyring')
    lines = path.read_text().splitlines() if path.exists() else []
    lines = [line for line in lines if not line.startswith(('DOCUMENTARY_ACTIVE_KEY_ID=', 'DOCUMENTARY_ENCRYPTION_KEYS='))]
    lines += [f'DOCUMENTARY_ACTIVE_KEY_ID={active}', "DOCUMENTARY_ENCRYPTION_KEYS='" + json.dumps(keys, separators=(',', ':')) + "'"]
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix='.documentary-env-')
    try:
        with os.fdopen(fd, 'w') as output:
            output.write('\n'.join(lines) + '\n')
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


if __name__ == '__main__':
    try:
        configure(Path('.env'))
    except Exception:
        print('Configuração documental inválida; nenhum segredo foi exibido.', file=sys.stderr)
        sys.exit(1)
