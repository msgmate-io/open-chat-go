#!/usr/bin/env bash

set -euo pipefail

bash ./scripts/dev_rebuild.sh >/proc/1/fd/1 2>/proc/1/fd/2
