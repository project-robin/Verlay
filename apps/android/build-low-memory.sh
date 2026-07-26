#!/usr/bin/env bash
set -euo pipefail

android_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
gradle_user_home="${GRADLE_USER_HOME:-/tmp/croc-gradle-home}"
go_module_cache="${GOMODCACHE:-/tmp/croc-go-mod-cache}"
go_build_cache="${GOCACHE:-/tmp/croc-android-go-cache}"

if [[ -n "${JAVA_HOME:-}" ]]; then
    java_home="$JAVA_HOME"
elif [[ -x /tmp/croc-jdk-full/bin/javac ]]; then
    java_home=/tmp/croc-jdk-full
elif command -v javac >/dev/null 2>&1; then
    java_home="$(dirname "$(dirname "$(readlink -f "$(command -v javac)")")")"
else
    java_home="$(dirname "$(dirname "$(readlink -f "$(command -v java)")")")"
fi

if [[ -n "${GRADLE_BIN:-}" ]]; then
    gradle_bin="$GRADLE_BIN"
elif [[ -x /tmp/gradle-9.5.0/bin/gradle ]]; then
    gradle_bin=/tmp/gradle-9.5.0/bin/gradle
else
    gradle_bin="$android_dir/gradlew"
fi

if [[ ! -x "$java_home/bin/javac" ]]; then
    echo "Missing Java compiler at $java_home/bin/javac" >&2
    echo "On Fedora, install java-25-openjdk-devel or set JAVA_HOME to a complete JDK." >&2
    exit 1
fi

if [[ ! -x "$gradle_bin" ]]; then
    echo "Missing Gradle at $gradle_bin" >&2
    echo "Restore gradlew or set GRADLE_BIN to a Gradle 9.5.0 executable." >&2
    exit 1
fi

export JAVA_HOME="$java_home"
export GRADLE_USER_HOME="$gradle_user_home"
export GOMODCACHE="$go_module_cache"
export GOCACHE="$go_build_cache"

cd "$android_dir"
exec "$gradle_bin" --no-daemon --max-workers=1 assembleDebug
