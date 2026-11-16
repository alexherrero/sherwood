#!/bin/sh
# Regenerate the helper's hash-locked dependency files from pyproject.toml.
# Needs uv (https://docs.astral.sh/uv/). Run it from anywhere; read the diff
# of every dependency change before committing it.
set -eu
cd "$(dirname "$0")"

uv pip compile pyproject.toml \
	--universal --generate-hashes --quiet \
	--custom-compile-command "brokerhelper/lock.sh" \
	--output-file requirements.lock

uv pip compile pyproject.toml --group dev \
	--universal --generate-hashes --quiet \
	--custom-compile-command "brokerhelper/lock.sh" \
	--output-file requirements-dev.lock
