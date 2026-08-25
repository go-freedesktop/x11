#!/usr/bin/env bash
# Copyright (c) the go-freedesktop/x11 authors.
# SPDX-License-Identifier: BSD-3-Clause
#
# The coverage gate: 100% of statements, on every platform the package builds
# for, with no exemptions.
#
# There is nothing here to exempt. Every line of this package is either pure
# protocol arithmetic or a syscall sitting behind a package variable a test can
# fail on purpose, which is the whole reason those seams exist. A number below
# 100 therefore means a branch nobody has written a test for, and not that the
# platform got in the way.
#
# It runs on Linux, darwin AND windows because the codec is portable: a
# protocol bug that only a big-endian or a no-unix-socket build would show is
# caught by whichever lane covers it, not by the one lane that has an X server.
set -euo pipefail

profile="${1:?usage: coverage-gate.sh <coverage profile>}"

if ! funcs=$(go tool cover -func="$profile"); then
  echo "::error::could not read the coverage profile $profile"
  exit 1
fi

below=$(printf '%s\n' "$funcs" | grep -v '100.0%' || true)
if [ -n "$below" ]; then
  echo "::error::these functions are below 100% coverage:"
  printf '%s\n' "$below"
  exit 1
fi

printf 'ok  every function is at 100%%\n\n'
printf '%s\n' "$funcs" | tail -1
