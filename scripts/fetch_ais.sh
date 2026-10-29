#!/usr/bin/env bash
# Usage: scripts/fetch_ais.sh [YYYY_MM_DD]   (default 2024_07_01)
# NOAA's zip files exist for 2023 and for July to December 2024. Earlier 2024 days return 404. Newer
# files are .csv.zst from the MarineCadastre AIS page: decompress with `zstd -d`, put the file in
# data/ and set AIS_FILE to its name.
set -euo pipefail
DAY="${1:-2024_07_01}"
YEAR="${DAY%%_*}"
mkdir -p data
curl -fL -o "data/AIS_${DAY}.zip" "https://coast.noaa.gov/htdata/CMSP/AISDataHandler/${YEAR}/AIS_${DAY}.zip"
unzip -o "data/AIS_${DAY}.zip" -d data
rm "data/AIS_${DAY}.zip"
ls -lh data
