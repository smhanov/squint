#!/bin/sh
# Fixture repositories are built by the Go test helper
# internal/git/testfixture — deterministic (pinned identity, pinned
# dates, isolated git configuration), so there is nothing to generate
# ahead of time. This script is a thin human-facing wrapper that runs the
# fixture package's tests, building every fixture once:
#
#   scripts/mkfixtures.sh -v
exec go test ./internal/git/testfixture "$@"
