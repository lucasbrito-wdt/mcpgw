#!/bin/sh
# mcpgw installer for Linux (systemd; apt/dnf) and macOS (launchd; Homebrew).
#   curl -fsSL https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.sh | sudo sh
#   sudo sh install.sh --help
set -eu

REPO_URL="https://github.com/lucasbrito-wdt/mcpgw"
DEFAULT_BASE_URL="$REPO_URL/releases/download"
SVC_NAME="mcpgw"
DB_NAME="mcpgw"
DB_ROLE="mcpgw"
PG_MAJOR=18
MAC_LABEL="io.github.lucasbrito-wdt.mcpgw"
MAC_PLIST="/Library/LaunchDaemons/$MAC_LABEL.plist"
UNIT_FILE="/etc/systemd/system/$SVC_NAME.service"
HEALTH_TIMEOUT=60

usage() {
  cat <<USAGE
Usage: install.sh [options]

  --version V            release to install (default: latest; required with a custom --base-url)
  --base-url URL|DIR     download source, files at <base>/v<version>/<file> (default: $DEFAULT_BASE_URL)
  --database-url URL     use an existing Postgres $PG_MAJOR+ database
  --install-postgres     install Postgres $PG_MAJOR locally and create role/db "$DB_ROLE"
  --public-url URL       public URL of the gateway (default: http://<listen-addr>)
  --listen-addr ADDR     listen address (default: 127.0.0.1:8787)
  --engine qjs|monty     sandbox engine (default: qjs)
  --admin-email E        create the first admin user
  --admin-password P     its password (prefer MCPGW_INSTALL_ADMIN_PASSWORD or the prompt: argv is visible in ps)
  --non-interactive      never prompt; fail on any missing required value
  --no-service           install binaries and config only
  --uninstall            stop and remove the service and binaries (keeps config and database)
  --purge                with --uninstall: also remove config, logs and the service user
  -h, --help             this help

Every option has an env var MCPGW_INSTALL_<NAME> (VERSION, BASE_URL, DATABASE_URL, INSTALL_POSTGRES=1,
PUBLIC_URL, LISTEN_ADDR, ENGINE, ADMIN_EMAIL, ADMIN_PASSWORD, NON_INTERACTIVE=1, NO_SERVICE=1).
The database is never dropped; uninstall prints how to do it by hand.
USAGE
}

# ---------- output ----------
if [ -t 1 ]; then
  C_RED=$(printf '\033[31m'); C_GRN=$(printf '\033[32m'); C_YEL=$(printf '\033[33m'); C_OFF=$(printf '\033[0m')
else
  C_RED=""; C_GRN=""; C_YEL=""; C_OFF=""
fi
info() { printf '%s==>%s %s\n' "$C_GRN" "$C_OFF" "$*"; }
warn() { printf '%swarning:%s %s\n' "$C_YEL" "$C_OFF" "$*" >&2; }
die() { printf '%serror:%s %s\n' "$C_RED" "$C_OFF" "$*" >&2; exit 1; }

TMP_DIR=""
cleanup() { [ -n "$TMP_DIR" ] && rm -rf "$TMP_DIR"; return 0; }
trap cleanup EXIT
trap 'exit 130' INT TERM

# ---------- options ----------
VERSION=${MCPGW_INSTALL_VERSION:-latest}
BASE_URL=${MCPGW_INSTALL_BASE_URL:-$DEFAULT_BASE_URL}
DATABASE_URL=${MCPGW_INSTALL_DATABASE_URL:-}
INSTALL_POSTGRES=${MCPGW_INSTALL_INSTALL_POSTGRES:-0}
PUBLIC_URL=${MCPGW_INSTALL_PUBLIC_URL:-}
LISTEN_ADDR=${MCPGW_INSTALL_LISTEN_ADDR:-127.0.0.1:8787}
ENGINE=${MCPGW_INSTALL_ENGINE:-qjs}
ADMIN_EMAIL=${MCPGW_INSTALL_ADMIN_EMAIL:-}
ADMIN_PASSWORD=${MCPGW_INSTALL_ADMIN_PASSWORD:-}
NON_INTERACTIVE=${MCPGW_INSTALL_NON_INTERACTIVE:-0}
NO_SERVICE=${MCPGW_INSTALL_NO_SERVICE:-0}
UNINSTALL=0
PURGE=0
NEW_CONFIG=0

parse_args() {
  while [ $# -gt 0 ]; do
    case $1 in
      --*=*) _k=${1%%=*}; _v=${1#*=}; shift; set -- "$_k" "$_v" "$@" ;;
    esac
    case $1 in
      --version|--base-url|--database-url|--public-url|--listen-addr|--engine|--admin-email|--admin-password)
        [ $# -ge 2 ] || die "$1 needs a value"
        case $1 in
          --version) VERSION=$2 ;;
          --base-url) BASE_URL=$2 ;;
          --database-url) DATABASE_URL=$2 ;;
          --public-url) PUBLIC_URL=$2 ;;
          --listen-addr) LISTEN_ADDR=$2 ;;
          --engine) ENGINE=$2 ;;
          --admin-email) ADMIN_EMAIL=$2 ;;
          --admin-password) ADMIN_PASSWORD=$2 ;;
        esac
        shift 2 ;;
      --install-postgres) INSTALL_POSTGRES=1; shift ;;
      --non-interactive) NON_INTERACTIVE=1; shift ;;
      --no-service) NO_SERVICE=1; shift ;;
      --uninstall) UNINSTALL=1; shift ;;
      --purge) PURGE=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) usage >&2; die "unknown option: $1" ;;
    esac
  done
  VERSION=${VERSION#v}
  case $ENGINE in qjs|monty) ;; *) die "--engine must be qjs or monty" ;; esac
  [ "$PURGE" = 0 ] || [ "$UNINSTALL" = 1 ] || die "--purge only makes sense with --uninstall"
  [ -z "$DATABASE_URL" ] || [ "$INSTALL_POSTGRES" = 0 ] || die "use either --database-url or --install-postgres, not both"
  [ -z "$ADMIN_PASSWORD" ] || [ -n "$ADMIN_EMAIL" ] || die "--admin-password needs --admin-email"
  if [ "$VERSION" = latest ] && [ "$BASE_URL" != "$DEFAULT_BASE_URL" ]; then
    die "--version is required with a custom --base-url"
  fi
  if [ "$INSTALL_POSTGRES" != 0 ] && [ "$INSTALL_POSTGRES" != 1 ]; then INSTALL_POSTGRES=1; fi
  return 0
}

# ---------- platform ----------
detect_platform() {
  case $(uname -s) in
    Linux) OS=linux ;;
    Darwin) OS=darwin ;;
    *) die "unsupported OS $(uname -s): this script covers Linux and macOS (use install.ps1 on Windows)" ;;
  esac
  case $(uname -m) in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture $(uname -m)" ;;
  esac
  if [ "$OS" = linux ]; then
    INSTALL_DIR=/opt/mcpgw
    CONF_DIR=/etc/mcpgw
    SVC_USER=mcpgw
    ROOT_GROUP=root
    LOG_DIR=""
  else
    INSTALL_DIR=/usr/local/mcpgw
    CONF_DIR=/usr/local/etc/mcpgw
    SVC_USER=_mcpgw
    ROOT_GROUP=wheel
    LOG_DIR=/usr/local/var/log/mcpgw
  fi
  BIN_DIR="$INSTALL_DIR/bin"
  ENV_FILE="$CONF_DIR/mcpgw.env"
  LINK=/usr/local/bin/mcpgw
}

require_root() {
  [ "$(id -u)" = 0 ] && return 0
  if [ -f "$0" ] && command -v sudo >/dev/null 2>&1; then
    info "root is required, re-running with sudo"
    exec sudo -E sh "$0" "$@"
  fi
  die "run as root: curl -fsSL <url>/install.sh | sudo sh   (or: sudo sh install.sh ...)"
}

have() { command -v "$1" >/dev/null 2>&1; }

# ---------- prompts (always from /dev/tty so curl | sh works) ----------
HAVE_TTY=0
( : </dev/tty ) 2>/dev/null && HAVE_TTY=1

can_prompt() { [ "$NON_INTERACTIVE" = 0 ] && [ "$HAVE_TTY" = 1 ]; }

# ask VAR "question" [default] [secret]; leaves VAR unchanged-or-default on empty answer
ask() {
  _var=$1; _q=$2; _def=${3:-}; _secret=${4:-}
  if [ -n "$_def" ]; then printf '%s [%s]: ' "$_q" "$_def" >/dev/tty; else printf '%s: ' "$_q" >/dev/tty; fi
  [ -z "$_secret" ] || stty -echo </dev/tty
  _ans=""
  if ! read -r _ans </dev/tty; then _ans=""; fi
  if [ -n "$_secret" ]; then stty echo </dev/tty; printf '\n' >/dev/tty; fi
  [ -n "$_ans" ] || _ans=$_def
  eval "$_var=\$_ans"
}

# ---------- secrets ----------
random_b64() { # 32 random bytes, base64
  if have openssl; then openssl rand -base64 32; else head -c 32 /dev/urandom | base64; fi
}
random_password() { # 32 alphanumeric chars
  head -c 96 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | cut -c1-32
}

# ---------- download ----------
fetch() { # fetch URL|PATH DEST
  case $1 in
    http://*|https://*)
      if have curl; then curl -fsSL --retry 3 -o "$2" "$1" || die "download failed: $1"
      elif have wget; then wget -q -O "$2" "$1" || die "download failed: $1"
      else die "curl or wget is required"; fi ;;
    *) [ -f "$1" ] || die "file not found: $1"; cp "$1" "$2" ;;
  esac
}

resolve_latest() {
  have curl || die "curl is required to resolve the latest version (or pass --version)"
  _url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$REPO_URL/releases/latest") || die "cannot resolve latest release"
  VERSION=${_url##*/v}
  [ -n "$VERSION" ] && [ "$VERSION" != "$_url" ] || die "cannot parse latest release from $_url"
  info "latest release: v$VERSION"
}

sha256_of() {
  if have sha256sum; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

download_release() {
  [ "$VERSION" != latest ] || resolve_latest
  ARCHIVE="mcpgw_${VERSION}_${OS}_${ARCH}.tar.gz"
  _base="${BASE_URL%/}/v$VERSION"
  TMP_DIR=$(mktemp -d)
  info "downloading $ARCHIVE"
  fetch "$_base/$ARCHIVE" "$TMP_DIR/$ARCHIVE"
  fetch "$_base/SHA256SUMS" "$TMP_DIR/SHA256SUMS"
  _want=$(awk -v f="$ARCHIVE" '$2 == f || $2 == "*" f { print $1; exit }' "$TMP_DIR/SHA256SUMS")
  [ -n "$_want" ] || die "$ARCHIVE not listed in SHA256SUMS"
  _got=$(sha256_of "$TMP_DIR/$ARCHIVE")
  [ "$_want" = "$_got" ] || die "checksum mismatch for $ARCHIVE (expected $_want, got $_got); aborting"
  info "checksum ok"
  mkdir "$TMP_DIR/x"
  tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR/x" || die "cannot extract $ARCHIVE"
  [ -f "$TMP_DIR/x/mcpgw" ] && [ -f "$TMP_DIR/x/montyrs-worker" ] || die "archive is missing mcpgw or montyrs-worker"
}

# ---------- service control ----------
systemd_ok() { [ "$OS" = linux ] && have systemctl && [ -d /run/systemd/system ]; }

stop_service() {
  if [ "$OS" = darwin ]; then
    if [ -f "$MAC_PLIST" ] && launchctl print "system/$MAC_LABEL" >/dev/null 2>&1; then
      launchctl bootout "system/$MAC_LABEL"
    fi
  elif systemd_ok && [ -f "$UNIT_FILE" ]; then
    if systemctl is-active --quiet "$SVC_NAME"; then systemctl stop "$SVC_NAME"; fi
  fi
}

# ---------- install ----------
install_binaries() {
  info "installing binaries to $BIN_DIR"
  mkdir -p "$BIN_DIR" /usr/local/bin
  chmod 0755 "$INSTALL_DIR" "$BIN_DIR"
  for _b in mcpgw montyrs-worker; do
    install -m 0755 "$TMP_DIR/x/$_b" "$BIN_DIR/$_b.new"
    mv -f "$BIN_DIR/$_b.new" "$BIN_DIR/$_b" # same directory: atomic replace
  done
  ln -sfn "$BIN_DIR/mcpgw" "$LINK"
}

mac_free_id() { # first id < 500 unused as both uid and gid, searching downward from 499
  _id=499
  while [ "$_id" -gt 200 ]; do
    if ! dscl . -list /Users UniqueID | awk -v i="$_id" '$2 == i { f = 1 } END { exit !f }' &&
       ! dscl . -list /Groups PrimaryGroupID | awk -v i="$_id" '$2 == i { f = 1 } END { exit !f }'; then
      echo "$_id"; return 0
    fi
    _id=$((_id - 1))
  done
  die "no free system uid/gid below 500"
}

ensure_user() {
  if [ "$OS" = linux ]; then
    id "$SVC_USER" >/dev/null 2>&1 && return 0
    _shell=/usr/sbin/nologin; [ -x "$_shell" ] || _shell=/sbin/nologin
    info "creating system user $SVC_USER"
    useradd --system --user-group --no-create-home --home-dir /nonexistent --shell "$_shell" "$SVC_USER"
    return 0
  fi
  dscl . -read "/Users/$SVC_USER" >/dev/null 2>&1 && return 0
  info "creating hidden user $SVC_USER"
  _id=$(mac_free_id)
  dscl . -create "/Groups/$SVC_USER"
  dscl . -create "/Groups/$SVC_USER" PrimaryGroupID "$_id"
  dscl . -create "/Groups/$SVC_USER" RealName "mcpgw service"
  dscl . -create "/Users/$SVC_USER"
  dscl . -create "/Users/$SVC_USER" UniqueID "$_id"
  dscl . -create "/Users/$SVC_USER" PrimaryGroupID "$_id"
  dscl . -create "/Users/$SVC_USER" UserShell /usr/bin/false
  dscl . -create "/Users/$SVC_USER" NFSHomeDirectory /var/empty
  dscl . -create "/Users/$SVC_USER" RealName "mcpgw service"
  dscl . -create "/Users/$SVC_USER" Password '*'
  dscl . -create "/Users/$SVC_USER" IsHidden 1
}

# ---------- postgres ----------
PG_PSQL=""
PG_PORT=5432

# shellcheck disable=SC1091
os_release() { ( . /etc/os-release; eval "echo \${$1:-}" ); }

pg_admin_psql() { # runs psql as the cluster superuser, reads SQL from stdin when no -c
  if [ "$OS" = darwin ]; then
    ( cd /tmp && sudo -H -u "$SUDO_USER" "$PG_PSQL" -p "$PG_PORT" -X -v ON_ERROR_STOP=1 -d postgres "$@" )
  else
    ( cd / && runuser -u postgres -- "$PG_PSQL" -p "$PG_PORT" -X -v ON_ERROR_STOP=1 -d postgres "$@" )
  fi
}

install_pg_debian() {
  info "adding the PGDG apt repository"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq curl ca-certificates >/dev/null
  _codename=$(os_release VERSION_CODENAME)
  [ -n "$_codename" ] || die "cannot determine the distribution codename"
  mkdir -p /usr/share/postgresql-common/pgdg
  curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc https://www.postgresql.org/media/keys/ACCC4CF8.asc
  echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt $_codename-pgdg main" \
    >/etc/apt/sources.list.d/pgdg.list
  apt-get update -qq
  info "installing postgresql-$PG_MAJOR"
  apt-get install -y -qq "postgresql-$PG_MAJOR" >/dev/null
  PG_PSQL=/usr/lib/postgresql/$PG_MAJOR/bin/psql
  _port=$(pg_lsclusters -h 2>/dev/null | awk -v v="$PG_MAJOR" '$1 == v { print $3; exit }')
  PG_PORT=${_port:-5432}
  # the package usually starts the cluster; wait_pg fails clearly if it did not
  if systemd_ok; then
    systemctl enable --now postgresql
  elif ! pg_lsclusters -h | awk -v v="$PG_MAJOR" '$1 == v && $4 == "online" { f = 1 } END { exit !f }'; then
    pg_ctlcluster "$PG_MAJOR" main start
  fi
}

install_pg_rhel() {
  _id=$(os_release ID); _ver=$(os_release VERSION_ID); _ver=${_ver%%.*}
  case $(uname -m) in aarch64) _rarch=aarch64 ;; *) _rarch=x86_64 ;; esac
  case $_id in
    fedora) _repo="F-$_ver-$_rarch"; _rpm=pgdg-fedora-repo-latest.noarch.rpm ;;
    *) _repo="EL-$_ver-$_rarch"; _rpm=pgdg-redhat-repo-latest.noarch.rpm ;;
  esac
  have runuser || dnf install -y -q util-linux >/dev/null
  info "adding the PGDG dnf repository ($_repo)"
  dnf install -y -q "https://download.postgresql.org/pub/repos/yum/reporpms/$_repo/$_rpm" ||
    die "PGDG has no repository for this distribution ($_repo); install Postgres $PG_MAJOR manually and use --database-url"
  if dnf -q module list postgresql >/dev/null 2>&1; then dnf -qy module disable postgresql >/dev/null; fi
  info "installing postgresql$PG_MAJOR-server"
  dnf install -y -q "postgresql$PG_MAJOR-server" || die "cannot install postgresql$PG_MAJOR-server"
  _pgbin=/usr/pgsql-$PG_MAJOR/bin
  _data=/var/lib/pgsql/$PG_MAJOR/data
  if [ ! -f "$_data/PG_VERSION" ]; then
    if systemd_ok; then
      "$_pgbin/postgresql-$PG_MAJOR-setup" initdb >/dev/null
    else # the setup helper reads the unit through systemctl
      ( cd / && runuser -u postgres -- "$_pgbin/initdb" -D "$_data" --auth-local=peer --auth-host=scram-sha-256 >/dev/null )
    fi
  fi
  PG_PSQL=$_pgbin/psql
  PG_PORT=5432
  if systemd_ok; then
    systemctl enable --now "postgresql-$PG_MAJOR"
  else
    ( cd / && runuser -u postgres -- "$_pgbin/pg_ctl" -D "$_data" -l "/var/lib/pgsql/$PG_MAJOR/startup.log" -w start >/dev/null )
  fi
}

install_pg_mac() {
  [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ] ||
    die "Homebrew refuses to run as root: run this installer with sudo from your normal user, or pass --database-url"
  _brew=""
  for _p in /opt/homebrew/bin/brew /usr/local/bin/brew; do [ -x "$_p" ] && _brew=$_p && break; done
  [ -n "$_brew" ] || die "Homebrew not found; install it from https://brew.sh or pass --database-url"
  info "installing postgresql@$PG_MAJOR with Homebrew (as $SUDO_USER)"
  ( cd /tmp && sudo -H -u "$SUDO_USER" "$_brew" install "postgresql@$PG_MAJOR" ) || die "brew install failed"
  ( cd /tmp && sudo -H -u "$SUDO_USER" "$_brew" services start "postgresql@$PG_MAJOR" ) || die "brew services start failed"
  PG_PSQL=$(cd /tmp && sudo -H -u "$SUDO_USER" "$_brew" --prefix "postgresql@$PG_MAJOR")/bin/psql
  PG_PORT=5432
}

wait_pg() {
  _i=0
  until pg_admin_psql -Atqc 'select 1' >/dev/null 2>&1; do
    _i=$((_i + 1)); [ "$_i" -le 30 ] || die "Postgres did not become ready"
    sleep 1
  done
}

provision_pg() {
  case $OS in
    darwin) install_pg_mac ;;
    linux)
      case "$(os_release ID) $(os_release ID_LIKE)" in
        *debian*|*ubuntu*) install_pg_debian ;;
        *rhel*|*fedora*|*centos*) install_pg_rhel ;;
        *) die "unsupported distribution for --install-postgres; use --database-url" ;;
      esac ;;
  esac
  wait_pg
  _pw=$(random_password)
  info "creating role $DB_ROLE and database $DB_NAME"
  pg_admin_psql -Atq >/dev/null <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$DB_ROLE') THEN
    CREATE ROLE $DB_ROLE LOGIN PASSWORD '$_pw';
  ELSE
    ALTER ROLE $DB_ROLE LOGIN PASSWORD '$_pw';
  END IF;
END \$\$;
SQL
  _exists=$(pg_admin_psql -Atqc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME'")
  [ -n "$_exists" ] || pg_admin_psql -Atqc "CREATE DATABASE $DB_NAME OWNER $DB_ROLE" >/dev/null
  DATABASE_URL="postgres://$DB_ROLE:$_pw@127.0.0.1:$PG_PORT/$DB_NAME?sslmode=disable"
}

validate_db_url() {
  if ! have psql; then
    warn "psql not found: skipping the connection check, 'mcpgw migrate' will report connection problems"
    return 0
  fi
  # shortcut: the URL is briefly visible in ps (psql takes no secret file); fine on a dedicated host
  _v=$(psql "$DATABASE_URL" -X -Atqc 'show server_version_num' 2>&1) ||
    die "cannot connect to the database: $_v"
  case $_v in ''|*[!0-9]*) die "unexpected server_version_num: $_v" ;; esac
  [ "$_v" -ge 180000 ] || die "Postgres 18+ is required (server_version_num=$_v)"
}

# ---------- config ----------
env_get() { sed -n "s/^$1=//p" "$ENV_FILE" | tail -1 | sed 's/^["'\'']//; s/["'\'']$//'; }

check_value() { # no characters that would break the env file format
  case $2 in
    *'"'*|*'
'*) die "$1 must not contain double quotes or newlines" ;;
  esac
}

choose_database() {
  [ -z "$DATABASE_URL" ] && [ "$INSTALL_POSTGRES" = 0 ] || return 0
  can_prompt || die "no database configured: pass --database-url or --install-postgres"
  _c=""
  printf 'Database:\n  1) use an existing Postgres %s+ (you provide the URL)\n  2) install Postgres %s on this machine\n' "$PG_MAJOR" "$PG_MAJOR" >/dev/tty
  ask _c "Choose" 2
  case $_c in
    1) ask DATABASE_URL "Postgres URL (postgres://user:pass@host:5432/db)" "" secret
       [ -n "$DATABASE_URL" ] || die "empty database URL" ;;
    2) INSTALL_POSTGRES=1 ;;
    *) die "invalid choice: $_c" ;;
  esac
}

write_config() {
  if [ -f "$ENV_FILE" ]; then
    info "keeping existing config $ENV_FILE"
    LISTEN_ADDR=$(env_get MCPGW_LISTEN_ADDR); LISTEN_ADDR=${LISTEN_ADDR:-127.0.0.1:8787}
    return 0
  fi
  choose_database
  if [ "$INSTALL_POSTGRES" = 1 ]; then provision_pg; else validate_db_url; fi
  if [ -z "$PUBLIC_URL" ]; then
    _def="http://$LISTEN_ADDR"
    PUBLIC_URL=$_def
    if can_prompt; then ask PUBLIC_URL "Public URL of the gateway" "$_def"; fi
  fi
  for _kv in "MCPGW_DATABASE_URL=$DATABASE_URL" "MCPGW_LISTEN_ADDR=$LISTEN_ADDR" "MCPGW_PUBLIC_URL=$PUBLIC_URL"; do
    check_value "${_kv%%=*}" "${_kv#*=}"
  done
  info "writing $ENV_FILE"
  NEW_CONFIG=1
  mkdir -p "$CONF_DIR"
  chown "root:$SVC_USER" "$CONF_DIR"
  chmod 0750 "$CONF_DIR"
  ( umask 077
    {
      printf 'MCPGW_DATABASE_URL="%s"\n' "$DATABASE_URL"
      printf 'MCPGW_LISTEN_ADDR="%s"\n' "$LISTEN_ADDR"
      printf 'MCPGW_PUBLIC_URL="%s"\n' "$PUBLIC_URL"
      printf 'MCPGW_MASTER_KEY="%s"\n' "$(random_b64)"
      printf 'MCPGW_SANDBOX_ENGINE="%s"\n' "$ENGINE"
    } >"$ENV_FILE.new" )
  mv -f "$ENV_FILE.new" "$ENV_FILE"
}

fix_config_perms() {
  chown "$SVC_USER" "$ENV_FILE"
  chmod 0600 "$ENV_FILE"
  chown "root:$SVC_USER" "$CONF_DIR"
  chmod 0750 "$CONF_DIR"
}

run_migrate() {
  info "running database migrations"
  if ! _out=$("$BIN_DIR/mcpgw" migrate --env-file "$ENV_FILE" 2>&1); then
    printf '%s\n' "$_out" >&2
    # A config written by this run is discarded so a corrected re-run is not stuck on "keeping existing config".
    if [ "$NEW_CONFIG" = 1 ]; then rm -f "$ENV_FILE"; die "migrations failed (see the error above); the new config was discarded, fix the database settings and re-run."; fi
    die "migrations failed (see the error above); existing config kept at $ENV_FILE."
  fi
}

create_admin() {
  if [ -z "$ADMIN_EMAIL" ]; then
    can_prompt || return 0
    ask ADMIN_EMAIL "First admin e-mail (blank to skip)"
    [ -n "$ADMIN_EMAIL" ] || return 0
  fi
  if [ -z "$ADMIN_PASSWORD" ]; then
    can_prompt || die "--admin-email needs a password: set MCPGW_INSTALL_ADMIN_PASSWORD"
    ask ADMIN_PASSWORD "Admin password" "" secret
    [ -n "$ADMIN_PASSWORD" ] || die "empty admin password"
  fi
  info "creating admin user $ADMIN_EMAIL"
  # password goes through the environment of the child only, never argv
  if ! _out=$(MCPGW_ADMIN_PASSWORD=$ADMIN_PASSWORD "$BIN_DIR/mcpgw" admin user create --env-file "$ENV_FILE" --email "$ADMIN_EMAIL" --admin 2>&1); then
    case $_out in
      *"already exists"*) warn "admin $ADMIN_EMAIL already exists, left untouched" ;;
      *) printf '%s\n' "$_out" >&2; die "could not create the admin user" ;;
    esac
  fi
}

# ---------- service ----------
# Hardening notes:
# - No SystemCallFilter: the montyrs worker installs its own seccomp filter.
# - No MemoryDenyWriteExecute: the qjs engine runs on wazero, whose default runtime config
#   is the JIT compiler (amd64/arm64) and needs writable+executable mappings.
write_unit() {
  cat >"$UNIT_FILE" <<UNIT
[Unit]
Description=mcpgw MCP gateway
After=network-online.target postgresql.service postgresql-$PG_MAJOR.service
Wants=network-online.target

[Service]
Type=simple
User=$SVC_USER
Group=$SVC_USER
WorkingDirectory=/
ExecStart=$BIN_DIR/mcpgw serve --env-file $ENV_FILE
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
UNIT
  chmod 0644 "$UNIT_FILE"
}

write_plist() {
  mkdir -p "$LOG_DIR"
  chown "$SVC_USER" "$LOG_DIR"
  chmod 0750 "$LOG_DIR"
  cat >"$MAC_PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$MAC_LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_DIR/mcpgw</string>
    <string>serve</string>
    <string>--env-file</string>
    <string>$ENV_FILE</string>
  </array>
  <key>UserName</key><string>$SVC_USER</string>
  <key>GroupName</key><string>$SVC_USER</string>
  <key>WorkingDirectory</key><string>/</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$LOG_DIR/mcpgw.log</string>
  <key>StandardErrorPath</key><string>$LOG_DIR/mcpgw.log</string>
</dict>
</plist>
PLIST
  chown "root:$ROOT_GROUP" "$MAC_PLIST"
  chmod 0644 "$MAC_PLIST"
}

start_service() {
  if [ "$OS" = darwin ]; then
    write_plist
    launchctl bootstrap system "$MAC_PLIST"
    launchctl enable "system/$MAC_LABEL"
    launchctl kickstart -k "system/$MAC_LABEL"
  else
    systemd_ok || die "systemd is not running; re-run with --no-service"
    write_unit
    systemctl daemon-reload
    systemctl enable "$SVC_NAME" >/dev/null 2>&1
    systemctl restart "$SVC_NAME"
  fi
}

wait_healthy() {
  _hp=${LISTEN_ADDR%:*}; _port=${LISTEN_ADDR##*:}
  case $_hp in ''|0.0.0.0|'[::]') _hp=127.0.0.1 ;; esac
  _url="http://$_hp:$_port/healthz"
  info "waiting for $_url"
  _i=0
  while [ "$_i" -lt "$HEALTH_TIMEOUT" ]; do
    if have curl; then curl -fsS -o /dev/null "$_url" 2>/dev/null && return 0
    else wget -q -O /dev/null "$_url" 2>/dev/null && return 0; fi
    _i=$((_i + 1)); sleep 1
  done
  [ "$OS" = linux ] && warn "last log lines: journalctl -u $SVC_NAME -n 30 --no-pager"
  die "gateway did not become healthy within ${HEALTH_TIMEOUT}s"
}

# ---------- uninstall ----------
do_uninstall() {
  info "uninstalling mcpgw"
  if [ "$OS" = darwin ]; then
    stop_service
    rm -f "$MAC_PLIST"
  else
    if systemd_ok; then
      if systemctl is-active --quiet "$SVC_NAME"; then systemctl stop "$SVC_NAME"; fi
      if systemctl is-enabled --quiet "$SVC_NAME" 2>/dev/null; then systemctl disable "$SVC_NAME" >/dev/null; fi
    fi
    rm -f "$UNIT_FILE"
    systemd_ok && systemctl daemon-reload
  fi
  if [ -L "$LINK" ] && [ "$(readlink "$LINK")" = "$BIN_DIR/mcpgw" ]; then rm -f "$LINK"; fi
  rm -rf "$INSTALL_DIR"
  if [ "$PURGE" = 1 ]; then
    warn "purging $CONF_DIR deletes MCPGW_MASTER_KEY: the data in the database can no longer be decrypted"
    rm -rf "$CONF_DIR"
    [ -z "$LOG_DIR" ] || rm -rf "$LOG_DIR"
    if [ "$OS" = linux ]; then
      if id "$SVC_USER" >/dev/null 2>&1; then userdel "$SVC_USER"; fi
      if getent group "$SVC_USER" >/dev/null 2>&1; then groupdel "$SVC_USER"; fi
    else
      if dscl . -read "/Users/$SVC_USER" >/dev/null 2>&1; then dscl . -delete "/Users/$SVC_USER"; fi
      if dscl . -read "/Groups/$SVC_USER" >/dev/null 2>&1; then dscl . -delete "/Groups/$SVC_USER"; fi
    fi
  else
    info "config kept at $CONF_DIR (use --purge to remove it)"
  fi
  cat <<MSG
The database was NOT touched. To drop it by hand (irreversible), as a Postgres superuser:
  DROP DATABASE $DB_NAME;
  DROP ROLE $DB_ROLE;
MSG
}

summary() {
  info "mcpgw v$VERSION installed"
  printf '  URL:        %s\n  config:     %s\n' "${PUBLIC_URL:-http://$LISTEN_ADDR}" "$ENV_FILE"
  if [ "$NO_SERVICE" = 1 ]; then
    printf '  service:    not installed (--no-service); run: %s serve --env-file %s\n' "$LINK" "$ENV_FILE"
  elif [ "$OS" = linux ]; then
    printf '  logs:       journalctl -u %s -f\n' "$SVC_NAME"
  else
    printf '  logs:       tail -f %s/mcpgw.log\n' "$LOG_DIR"
  fi
  printf '  uninstall:  sudo sh install.sh --uninstall [--purge]\n'
}

main() {
  parse_args "$@"
  detect_platform
  require_root "$@"
  if [ "$UNINSTALL" = 1 ]; then do_uninstall; return 0; fi
  download_release
  stop_service
  install_binaries
  ensure_user
  write_config
  fix_config_perms
  run_migrate
  create_admin
  if [ "$NO_SERVICE" = 0 ]; then
    start_service
    wait_healthy
  fi
  summary
}

main "$@"
