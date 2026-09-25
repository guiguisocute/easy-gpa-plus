#!/bin/sh
set -eu
# A named volume keeps hashed files for tabs opened before an image update.
mkdir -p /var/cache/easygpa/assets
if [ -d /usr/share/nginx/html/assets ]; then
  find /usr/share/nginx/html/assets -type f -exec sh -c '
    for asset do
      relative=${asset#/usr/share/nginx/html/assets/}
      destination=/var/cache/easygpa/assets/$relative
      mkdir -p "$(dirname "$destination")"
      cp -n "$asset" "$destination"
    done
  ' sh {} +
fi
