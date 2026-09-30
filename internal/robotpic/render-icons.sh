#!/usr/bin/env bash
# Render the robot-screen icons (png/*.png, 160 px) from the page's emoji SVGs.
# Needs inkscape. Run after adding a food or changing an icon.
set -euo pipefail
cd "$(dirname "$0")"
SVG=../app/ui/emoji
for f in apple carrot banana cake milk bread heart battery ball star moon; do
    inkscape "$SVG/$f.svg" --export-type=png --export-width=160 --export-filename="png/$f.png" >/dev/null 2>&1
done
