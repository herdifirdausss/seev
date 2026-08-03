#!/usr/bin/env sh
set -eu

umask 077
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
secret_dir="$script_dir/../secrets"
mkdir -p "$secret_dir"

create_secret() {
  name=$1
  bytes=$2
  file="$secret_dir/$name"
  if [ ! -s "$file" ]; then
    openssl rand -hex "$bytes" > "$file"
    chmod 600 "$file"
    printf 'generated %s\n' "$file"
  else
    printf 'preserved %s\n' "$file"
  fi
}

create_secret pseudonym_salt 32
create_secret bi_password 24
create_secret dbt_password 24
create_secret reconciliation_password 24
