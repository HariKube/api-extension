#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <decision-maker-config-path> <command> [args...]" >&2
  exit 1
fi

config_path="$1"
shift

provider="${DECISION_MAKER_PROVIDER:-mock}"
timeout="${DECISION_MAKER_TIMEOUT:-30s}"

backup_path="$(mktemp)"
cp "$config_path" "$backup_path"

cleanup() {
  cp "$backup_path" "$config_path"
  rm -f "$backup_path"
}
trap cleanup EXIT

python - "$config_path" "$provider" "$timeout" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
provider = sys.argv[2]
timeout = sys.argv[3]
lines = path.read_text().splitlines()
found_provider = False
found_timeout = False
for index, line in enumerate(lines):
    if line.startswith("  provider: "):
        lines[index] = f"  provider: {provider}"
        found_provider = True
    elif line.startswith("  timeout: "):
        lines[index] = f"  timeout: {timeout}"
        found_timeout = True

if not found_provider:
    raise SystemExit(f"provider field not found in {path}")
if not found_timeout:
    raise SystemExit(f"timeout field not found in {path}")

path.write_text("\n".join(lines) + "\n")
PY

"$@"
