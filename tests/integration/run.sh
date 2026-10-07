#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
"${script_dir}/real-docker.sh"
"${script_dir}/image-refresh.sh"
"${script_dir}/runtime-contracts.sh"
"${script_dir}/session-transfer.sh"
