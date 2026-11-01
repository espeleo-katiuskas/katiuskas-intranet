#!/usr/bin/env bash
# Generates the thumbnails of the federative license copies in the people's own
# files: for every <root>/<login>/licencias/yyyy-mm-dd-tipo.ext (a PDF or an
# image) that has no thumbnail yet, or a thumbnail older than the file, writes
# <root>/<login>/licencias/.thumbnails/<same filename>.jpg, at most 280 pixels
# on its longest side (twice the size /perfil and /socio display it at, so
# it stays sharp on a high-DPI screen; an image already smaller than that is
# not enlarged). Nothing in the app generates them: the
# page falls back to the original file for an image, and to a 📄 placeholder
# for a PDF with no thumbnail.
#
# Usage:
#   scripts/generate-license-thumbnails.sh [options] [login...]
#
# Options:
#   --root DIR   the user-files directory (default: $USER_FILES_ROOT, or
#                ./user-files next to docker-compose.yml)
#   --size N     pixels on the longest side (default 280)
#   --force      regenerate even if the thumbnail is up to date
#   --dry-run    only say what would be generated or removed
#   -h, --help   this text
# With logins, only those people's licencias/ are looked at.
#
# Only files the page actually lists get one: named yyyy-mm-dd-<type>.<ext>
# with a pdf, png, jpg, jpeg, gif or webp extension (an svg scales by itself).
#
# It also tidies each .thumbnails: whatever is in there that is not the
# thumbnail (<filename>.jpg) of a file that is still in the licencias directory
# and that the page lists is removed — the thumbnail of a file that was deleted
# or renamed, anything else that was dropped in, subdirectories included. Only
# a temporary file less than an hour old is left alone, in case another run is
# writing it right now. With --dry-run it only says what it would remove.
#
# Run it as the user that owns the files, so the new ones belong to them too.
#
# Needs, depending on what there is to convert:
#   PDFs    pdftoppm (poppler-utils), or ImageMagick plus Ghostscript
#   images  ImageMagick (magick, or convert) — or vipsthumbnail (libvips)
# e.g. on Debian: apt install poppler-utils imagemagick
set -euo pipefail

root="${USER_FILES_ROOT:-}"
size=280
force=0
dry_run=0
logins=()

usage() { sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'; }
die() { echo "error: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --root) [ $# -ge 2 ] || die "--root needs a directory"; root="$2"; shift 2 ;;
    --size) [ $# -ge 2 ] || die "--size needs a number"; size="$2"; shift 2 ;;
    --force) force=1; shift ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) die "unknown option: $1 (see --help)" ;;
    *) logins+=("$1"); shift ;;
  esac
done

# A --root (or USER_FILES_ROOT) is relative to where the script is run from;
# the default is next to docker-compose.yml, wherever that is from here.
if [ -z "$root" ]; then
  root="$(dirname "${BASH_SOURCE[0]}")/../user-files"
fi

case "$size" in ''|*[!0-9]*|0) die "--size must be a positive number" ;; esac
[ -d "$root" ] || die "no such directory: $root (use --root, or set USER_FILES_ROOT)"

# The tool for each kind, found lazily: only complain about a missing one if
# there is a file that needs it.
imagemagick=""
for candidate in magick convert; do
  if command -v "$candidate" >/dev/null 2>&1; then imagemagick="$candidate"; break; fi
done
have_pdftoppm=0; command -v pdftoppm >/dev/null 2>&1 && have_pdftoppm=1
have_vips=0; command -v vipsthumbnail >/dev/null 2>&1 && have_vips=1

# convert_image SRC DEST — a JPEG of an image, first frame only, rotated as
# its EXIF says, with any transparency flattened onto white.
convert_image() {
  local src="$1" dest="$2"
  if [ -n "$imagemagick" ]; then
    "$imagemagick" "${src}[0]" -auto-orient -thumbnail "${size}x${size}>" \
      -background white -alpha remove -alpha off -quality 85 "jpg:$dest"
  elif [ "$have_vips" = 1 ]; then
    vipsthumbnail "$src" --size "${size}x${size}" -o "${dest}[Q=85]"
  else
    return 127
  fi
}

# convert_pdf SRC DEST — a JPEG of the first page.
convert_pdf() {
  local src="$1" dest="$2" base
  if [ "$have_pdftoppm" = 1 ]; then
    base="${dest%.jpg}"
    pdftoppm -jpeg -jpegopt quality=85 -scale-to "$size" -f 1 -l 1 -singlefile "$src" "$base"
  elif [ -n "$imagemagick" ]; then
    "$imagemagick" -density 150 "${src}[0]" -thumbnail "${size}x${size}>" \
      -background white -alpha remove -alpha off -quality 85 "jpg:$dest"
  else
    return 127
  fi
}

made=0; fresh=0; failed=0; removed=0; need_pdf=0; need_image=0

# kind_of NAME — prints pdf or image for a file name the page lists
# (yyyy-mm-dd-<something>.<known extension>), and fails for any other.
kind_of() {
  local lower
  lower="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
  case "$lower" in
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-?*.pdf) echo pdf ;;
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-?*.png|\
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-?*.jpg|\
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-?*.jpeg|\
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-?*.gif|\
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-?*.webp) echo image ;;
    *) return 1 ;;
  esac
}

process() {
  local file="$1" dir name kind thumbs thumb tmp status
  dir="$(dirname "$file")"
  name="$(basename "$file")"
  # Only what the page lists.
  kind="$(kind_of "$name")" || return 0

  thumbs="$dir/.thumbnails"
  thumb="$thumbs/$name.jpg"
  if [ "$force" = 0 ] && [ -f "$thumb" ] && [ ! "$file" -nt "$thumb" ]; then
    fresh=$((fresh + 1))
    return 0
  fi

  if [ "$dry_run" = 1 ]; then
    echo "would generate $thumb"
    made=$((made + 1))
    return 0
  fi

  mkdir -p "$thumbs"
  # Written under a temporary name and moved into place, so a page loading it
  # meanwhile never gets half a file, and a failure leaves no broken one. The
  # name keeps the .jpg extension some tools decide the format from.
  tmp="$thumbs/.tmp-$$-$RANDOM.jpg"
  status=0
  if [ "$kind" = pdf ]; then convert_pdf "$file" "$tmp" || status=$?; else convert_image "$file" "$tmp" || status=$?; fi
  if [ "$status" = 127 ]; then
    rm -f "$tmp"
    if [ "$kind" = pdf ]; then need_pdf=1; else need_image=1; fi
    echo "skipped $file: no tool to convert it" >&2
    failed=$((failed + 1))
  elif [ "$status" != 0 ] || [ ! -s "$tmp" ]; then
    rm -f "$tmp"
    echo "failed: $file" >&2
    failed=$((failed + 1))
  else
    mv -f "$tmp" "$thumb"
    echo "generated $thumb"
    made=$((made + 1))
  fi
}

if [ ${#logins[@]} -gt 0 ]; then
  dirs=()
  for login in "${logins[@]}"; do
    case "$login" in */*|.*) die "not a login: $login" ;; esac
    dirs+=("$root/$login/licencias")
  done
else
  dirs=("$root"/*/licencias)
fi

# clean DIR — removes from DIR/.thumbnails whatever is not the thumbnail of a file in DIR that the
# page lists: the thumbnail of a file that is gone, any other file, a link, a subdirectory. A
# temporary file of this script is left if it is less than an hour old (another run may be writing
# it). A .thumbnails that is itself a link is left alone: it could lead anywhere.
clean() {
  local dir="$1" thumbs entry base name
  thumbs="$dir/.thumbnails"
  [ -e "$thumbs" ] || [ -L "$thumbs" ] || return 0
  if [ -L "$thumbs" ] || [ ! -d "$thumbs" ]; then
    echo "not tidied: $thumbs is not a directory" >&2
    return 0
  fi
  while IFS= read -r -d '' entry; do
    base="$(basename "$entry")"
    if [ -f "$entry" ] && [ ! -L "$entry" ]; then
      case "$base" in
        .tmp-*.jpg)
          # A recent one belongs to a run that may still be going; an old one is a leftover.
          [ -z "$(find "$entry" -maxdepth 0 -mmin -60)" ] || continue ;;
        *.jpg)
          name="${base%.jpg}"
          if [ -f "$dir/$name" ] && kind_of "$name" >/dev/null; then continue; fi ;;
      esac
    fi
    if [ "$dry_run" = 1 ]; then
      echo "would remove $entry"
    else
      rm -rf -- "$entry"
      echo "removed $entry"
    fi
    removed=$((removed + 1))
  done < <(find "$thumbs" -mindepth 1 -maxdepth 1 -print0)
}

for dir in "${dirs[@]}"; do
  [ -d "$dir" ] || continue
  for file in "$dir"/*; do
    [ -f "$file" ] || continue
    process "$file"
  done
  clean "$dir"
done

verb="generated"; removed_verb="removed"
if [ "$dry_run" = 1 ]; then verb="to generate"; removed_verb="to remove"; fi
echo "$made $verb, $fresh already up to date, $removed $removed_verb, $failed failed."
if [ "$need_pdf" = 1 ] || [ "$need_image" = 1 ]; then
  echo "Install what's missing, e.g. on Debian: apt install poppler-utils imagemagick" >&2
  [ "$need_pdf" = 1 ] && echo "  PDFs need pdftoppm (poppler-utils), or ImageMagick with Ghostscript." >&2
  [ "$need_image" = 1 ] && echo "  Images need ImageMagick (magick or convert), or vipsthumbnail (libvips)." >&2
fi
[ "$failed" = 0 ]
