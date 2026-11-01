#!/bin/sh
# Regenerates the raster icons from the SVG sources in this folder. Needs ImageMagick (`magick`).
set -eu
cd "$(dirname "$0")"
M="${MAGICK:-magick}"
r() { "$M" -background none -density 1200 "$1" -resize "$2x$2" "$3"; }
cp icon.svg ../app/icon.svg
r icon.svg 16 /tmp/mcpgw-16.png
r icon.svg 32 /tmp/mcpgw-32.png
"$M" /tmp/mcpgw-16.png /tmp/mcpgw-32.png ../app/favicon.ico
rm -f /tmp/mcpgw-16.png /tmp/mcpgw-32.png
r icon-fullbleed.svg 180 ../app/apple-icon.png
r icon-standard.svg 192 ../public/icon-192.png
r icon-standard.svg 512 ../public/icon-512.png
r icon-maskable.svg 512 ../public/icon-maskable-512.png
