#!/usr/bin/env bash
set -euo pipefail

appimage="/home/map/project-z/new-tempo/file-sharing-2offical/croc/apps/desktop/src-tauri/target/release/bundle/appimage/Croc Transfer_0.1.0_amd64.AppImage"
exec "$appimage"
