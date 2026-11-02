#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Deletes the lab's kind cluster.
set -euo pipefail
kind delete cluster --name stormbreaker
