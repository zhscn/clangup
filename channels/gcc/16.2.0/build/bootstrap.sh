#!/usr/bin/env bash
set -euo pipefail

make=$1
jobs=$2
stage=$3

case "$stage" in
    bootstrap)
        "$make" -j"$jobs" bootstrap
        ;;
    profile)
        printf '%s\n' stagefeedback > stage_final
        "$make" -j"$jobs" stageprofile-bubble
        exit 0
        ;;
    train)
        "$make" -j"$jobs" stagetrain-bubble
        exit 0
        ;;
    feedback)
        "$make" -j"$jobs" profiledbootstrap
        ;;
    *) echo "Unknown GCC bootstrap stage: $stage" >&2; exit 2 ;;
esac
"$make" install
