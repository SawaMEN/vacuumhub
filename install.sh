#!/usr/bin/env bash
# VacuumHub native installer: Linux/systemd ONLY. No Docker or localhost mode.
set -Eeuo pipefail
REPO=SawaMEN/vacuumhub
INSTALL_DIR=/opt/vacuumhub
CONFIG_DIR=/etc/vacuumhub
STATE_DIR=/var/lib/vacuumhub
ENV_FILE=/etc/vacuumhub/environment
BIN=/opt/vacuumhub/vacuumhub
MANAGER=/usr/local/bin/vacuumhub
SERVICE=/etc/systemd/system/vacuumhub.service
CERT_DIR=/etc/vacuumhub/tls
SCRIPT_DIR=''
[[ -z "${BASH_SOURCE[0]:-}" ]] || SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || true)"

die(){ echo "Ошибка: $*" >&2; exit 1; }
require_root(){ [[ ${EUID:-$(id -u)} -eq 0 ]] || die 'Запустите установщик через sudo.'; }
check_system(){
 [[ "$(uname -s)" == Linux ]] || die 'Требуется Linux.'
 command -v systemctl >/dev/null && [[ -d /run/systemd/system ]] || die 'Нужен запущенный systemd.'
 case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;;
  *) die 'Поддерживаются только amd64 и arm64.';;
 esac
 for tool in curl tar openssl base64 sha256sum awk sed;do
  command -v "$tool" >/dev/null || die "Не найдена команда $tool."
 done
}
get_config(){
 local key="$1"
 [[ -f "$ENV_FILE" ]] || return 0
 sed -n "s/^${key}=//p" "$ENV_FILE" | head -n 1
}
ask(){
 local var="$1" prompt="$2" def="$3" v=''
 if [[ "${VACUUMHUB_NONINTERACTIVE:-0}" == 1 ]]; then
  printf -v "$var" '%s' "$def";return
 fi
 [[ -r /dev/tty ]] || die 'Нужен терминал для выбора параметров (или VACUUMHUB_NONINTERACTIVE=1).'
 read -r -p "$prompt [$def]: " v </dev/tty || die 'Ввод прерван.'
 printf -v "$var" '%s' "${v:-$def}"
}
ask_password(){
 local text=''
 if [[ "${VACUUMHUB_NONINTERACTIVE:-0}" == 1 ]]; then
  text="${VACUUMHUB_ADMIN_PASSWORD:-}"
 else
  read -r -s -p 'Пароль администратора (Enter = создать случайный): ' text </dev/tty || die 'Ввод прерван.'
  echo
 fi
 GENERATED_PASSWORD=''
 if [[ -z "$text" ]];then
  text="$(openssl rand -hex 20)"
  GENERATED_PASSWORD="$text"
 fi
 [[ ${#text} -ge 12 && ${#text} -le 256 && "$text" != *$'\n'* && "$text" != *$'\r'* ]] ||
   die 'Пароль должен содержать от 12 до 256 символов и не иметь перевода строки.'
 PASSWORD_B64="$(printf '%s' "$text" | base64 | tr -d '\r\n')"
}
valid_port(){
 [[ "$1" =~ ^[0-9]{1,5}$ ]] && ((10#$1>=1 && 10#$1<=65535))
}
valid_ip(){
 local input="$1" octet
 if [[ "$input" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]];then
  IFS=. read -r -a parts <<< "$input"
  for octet in "${parts[@]}";do ((10#$octet<=255)) || return 1;done
  [[ "$input" != 127.* && "$input" != 10.* && "$input" != 192.168.* && "$input" != 169.254.* && "$input" != 0.* ]] || return 1
  return 0
 fi
 if [[ "$input" == *:* ]];then
  if ! command -v python3 >/dev/null;then
   echo 'Для проверки IPv6 нужен python3; установите его или выберите IPv4.' >&2;return 1
  fi
  python3 -c 'import ipaddress,sys; ip=ipaddress.ip_address(sys.argv[1]); assert ip.version==6 and ip.is_global' "$input" 2>/dev/null
  return
 fi
 return 1
}
valid_domain(){
 local d="$1"
 [[ "$d" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z][A-Za-z0-9.-]*$ && ${#d} -le 253 ]] || return 1
 [[ "$d" != *..* && "$d" != *.-* && "$d" != *-.*
    && "$d" != *.local && "$d" != *.localhost && "$d" != *.internal ]] || return 1
}
suggest_ip(){
 curl -fsS --connect-timeout 2 --max-time 4 https://api64.ipify.org 2>/dev/null || true
}
config_access(){
 local choice suggested defmode old_mode old_host choice_tls
 old_mode="$(get_config ACCESS_MODE)"
 old_host="$(get_config PUBLIC_HOST)"
 defmode=1
 [[ "$old_mode" != domain ]] || defmode=2
 echo
 echo 'Выберите адрес доступа к VacuumHub:'
 echo '1) По IP-адресу (HTTPS)'
 echo '2) По домену (HTTPS)'
 echo '0) Назад'
 if [[ "${VACUUMHUB_NONINTERACTIVE:-0}" == 1 ]];then
  case "${VACUUMHUB_MODE:-}" in ip) choice=1;; domain) choice=2;; *) die 'Задайте VACUUMHUB_MODE=ip или domain.';; esac
 else ask choice 'Режим' "$defmode";fi
 case "$choice" in 1) ACCESS_MODE=ip;; 2) ACCESS_MODE=domain;; 0) return 2;; *) die 'Выберите IP или домен.';;esac
 suggested=''
 if [[ "$old_mode" == "$ACCESS_MODE" ]];then suggested="$old_host"
 elif [[ "$ACCESS_MODE" == ip ]];then suggested="$(suggest_ip)";fi
 ask PUBLIC_HOST 'Адрес (без https:// и порта)' "${VACUUMHUB_HOST:-$suggested}"
 if [[ "$ACCESS_MODE" == ip ]];then
  valid_ip "$PUBLIC_HOST" || die 'Нужен внешний IPv4 или IPv6, не localhost.'
 else
  valid_domain "$PUBLIC_HOST" || die 'Укажите доменное имя FQDN, не IP и не localhost.'
 fi
 ask LISTEN_PORT 'HTTPS TCP-порт панели' "${VACUUMHUB_PORT:-$(get_config LISTEN_PORT)}"
 LISTEN_PORT="${LISTEN_PORT:-8443}"
 valid_port "$LISTEN_PORT" || die 'TCP-порт должен быть от 1 до 65535.'
 LISTEN_PORT="$((10#$LISTEN_PORT))"
 TLS_MODE="${VACUUMHUB_TLS_MODE:-}"
 if [[ -z "$TLS_MODE" ]];then
  echo 'Сертификат HTTPS:'
  echo '1) Let’s Encrypt (домен или публичный IP; нужен свободный TCP 80)'
  echo '2) Самоподписанный (браузер потребует ручного доверия)'
  echo '3) Готовые файлы сертификата и ключа PEM'
  ask choice_tls 'Выберите сертификат' 1
  case "$choice_tls" in 1) TLS_MODE=letsencrypt;; 2) TLS_MODE=selfsigned;; 3) TLS_MODE=existing;; *) die 'Неизвестный вариант сертификата.';; esac
 fi
 case "$TLS_MODE" in letsencrypt|selfsigned|existing) ;; *) die 'TLS: letsencrypt, selfsigned или existing.';;esac
 TLS_CERT_SOURCE="${VACUUMHUB_TLS_CERT:-}"
 TLS_KEY_SOURCE="${VACUUMHUB_TLS_KEY:-}"
 if [[ "$TLS_MODE" == existing ]];then
  ask TLS_CERT_SOURCE 'Путь к PEM-сертификату' "$TLS_CERT_SOURCE"
  ask TLS_KEY_SOURCE 'Путь к PEM-ключу' "$TLS_KEY_SOURCE"
  [[ "$TLS_CERT_SOURCE" == /* && "$TLS_KEY_SOURCE" == /* && -r "$TLS_CERT_SOURCE" && -r "$TLS_KEY_SOURCE" ]] ||
    die 'Укажите существующие абсолютные пути к PEM-файлам.'
 fi
}
config_credentials(){
 local default_user
 default_user="$(get_config ADMIN_BASIC_USER)"
 ask ADMIN_BASIC_USER 'Логин администратора' "${VACUUMHUB_ADMIN_USER:-${default_user:-admin}}"
 [[ "$ADMIN_BASIC_USER" =~ ^[A-Za-z][A-Za-z0-9_.-]{1,47}$ ]] || die 'Логин: 2–48 символов (буквы, цифры, _ . -).'
 ask_password
}
prepare_directories(){
 if ! id vacuumhub >/dev/null 2>&1;then
  useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin --user-group vacuumhub
 fi
 install -d -m 0755 "$INSTALL_DIR"
 install -d -m 0700 "$CONFIG_DIR" "$STATE_DIR"
 install -d -m 0750 -o root -g vacuumhub "$CERT_DIR"
 install -d -m 0700 /var/log/vacuumhub
 chown vacuumhub:vacuumhub "$STATE_DIR"
}
install_cert_copy(){
 local original="$1" original_key="$2"
 [[ -s "$original" && -s "$original_key" ]] || die 'Отсутствует сертификат или ключ.'
 install -m 0644 -o root -g vacuumhub "$original" "$CERT_DIR/cert.pem.new"
 install -m 0640 -o root -g vacuumhub "$original_key" "$CERT_DIR/key.pem.new"
 mv -f "$CERT_DIR/cert.pem.new" "$CERT_DIR/cert.pem"
 mv -f "$CERT_DIR/key.pem.new" "$CERT_DIR/key.pem"
}
cert_name(){ printf 'vacuumhub-%s' "$(printf %s "$PUBLIC_HOST" | sha256sum | cut -c1-12)"; }
prepare_tls(){
 local acme_dir="/etc/vacuumhub/acme" acme_work="/var/lib/vacuumhub/acme"
 if [[ "$TLS_MODE" == selfsigned ]];then
  local san
  san="DNS:$PUBLIC_HOST"
  [[ "$ACCESS_MODE" != ip ]] || san="IP:$PUBLIC_HOST"
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -sha256 -nodes -days 365 \
    -subj '/CN=VacuumHub' -addext "subjectAltName=$san" \
    -keyout "$TMP/cert-key.pem" -out "$TMP/cert.pem" >/dev/null 2>&1 || die 'Не удалось создать TLS сертификат.'
  install_cert_copy "$TMP/cert.pem" "$TMP/cert-key.pem"
  CERT_NAME=''
  echo 'ВНИМАНИЕ: самоподписанный сертификат не доверен браузером по умолчанию.'
 elif [[ "$TLS_MODE" == existing ]];then
  install_cert_copy "$TLS_CERT_SOURCE" "$TLS_KEY_SOURCE"
  CERT_NAME=''
 else
  command -v certbot >/dev/null || die 'Не найден Certbot. Установите его или выберите самоподписанный сертификат.'
  if [[ "$ACCESS_MODE" == ip ]];then
    local help
    help="$(certbot --help all)"
    [[ "$help" == *--ip-address* && "$help" == *--preferred-profile* ]] ||
      die 'Для доверенных IP-сертификатов нужен Certbot >=5.3; обновите его или выберите самоподписанный.'
  fi
  CERT_NAME="$(cert_name)"
  mkdir -p "$acme_dir" "$acme_work"
  local args=(certonly --standalone --non-interactive --agree-tos --register-unsafely-without-email
    --cert-name "$CERT_NAME" --config-dir "$acme_dir" --work-dir "$acme_work"
    --logs-dir /var/log/vacuumhub)
  if [[ "$ACCESS_MODE" == ip ]];then
    args+=(--ip-address "$PUBLIC_HOST" --preferred-profile shortlived)
  else args+=(-d "$PUBLIC_HOST");fi
  echo 'Для Let’s Encrypt требуется входящий TCP 80, не занятый другим веб-сервером.'
  certbot "${args[@]}" || die 'Не удалось получить сертификат. Проверьте DNS/IP, TCP 80 или используйте готовый PEM.'
  install_cert_copy "$acme_dir/live/$CERT_NAME/fullchain.pem" "$acme_dir/live/$CERT_NAME/privkey.pem"
 fi
}
write_environment(){
 local output="$1" LISTEN_INTERFACE='0.0.0.0'
 [[ "$PUBLIC_HOST" != *:* ]] || LISTEN_INTERFACE='::'
 local token="$(get_config ADMIN_API_TOKEN)"
 [[ "$token" =~ ^[0-9a-f]{64}$ ]] || token="$(openssl rand -hex 32)"
 cat > "$output" <<ENV
DATA_DIR=/var/lib/vacuumhub
ADMIN_API_TOKEN=$token
ADMIN_BASIC_USER=$ADMIN_BASIC_USER
ADMIN_BASIC_PASSWORD_B64=$PASSWORD_B64
ACCESS_MODE=$ACCESS_MODE
PUBLIC_HOST=$PUBLIC_HOST
LISTEN_HOST=$LISTEN_INTERFACE
LISTEN_PORT=$LISTEN_PORT
TLS_CERT_FILE=/etc/vacuumhub/tls/cert.pem
TLS_KEY_FILE=/etc/vacuumhub/tls/key.pem
FIRMWARE_CLAMD_ADDR=$(get_config FIRMWARE_CLAMD_ADDR)
FIRMWARE_TRUSTED_KEYS=$(get_config FIRMWARE_TRUSTED_KEYS)
TLS_MODE=$TLS_MODE
CERT_NAME=$CERT_NAME
ENV
 chmod 0600 "$output"
}
fetch_package(){
 local name="vacuumhub-linux-$ARCH.tar.gz" url expected actual
 mkdir -p "$TMP/dist"
 if [[ -n "${VACUUMHUB_PACKAGE:-}" ]];then
  [[ -s "$VACUUMHUB_PACKAGE" && "${VACUUMHUB_PACKAGE_SHA256:-}" =~ ^[0-9a-fA-F]{64}$ ]] ||
    die 'Для локального пакета укажите VACUUMHUB_PACKAGE и SHA256.'
  cp "$VACUUMHUB_PACKAGE" "$TMP/dist/$name"
  expected="$VACUUMHUB_PACKAGE_SHA256"
 else
  url="https://github.com/$REPO/releases/latest/download"
  curl -fLsS --retry 3 --connect-timeout 15 "$url/SHA256SUMS" -o "$TMP/SHA256SUMS" ||
    die 'Пока нет готового релиза. После первой зелёной сборки появится установочный пакет.'
  expected="$(awk -v f="$name" '$2==f {print $1}' "$TMP/SHA256SUMS")"
  [[ "$expected" =~ ^[a-f0-9]{64}$ ]] || die 'Не найден SHA-256 для архитектуры.'
  curl -fLsS --retry 3 --connect-timeout 15 "$url/$name" -o "$TMP/dist/$name" ||
    die 'Не удалось скачать архив готового релиза.'
 fi
 actual="$(sha256sum "$TMP/dist/$name" | awk '{print $1}')"
 [[ "${actual,,}" == "${expected,,}" ]] || die 'SHA-256 загруженного релиза не совпадает.'
 local entry first
 while IFS= read -r entry;do
  case "$entry" in
   vacuumhub|install.sh|deploy/|deploy/vacuumhub|deploy/vacuumhub.service|deploy/vacuumhub-cert-renew.service|deploy/vacuumhub-cert-renew.timer|scripts/|scripts/backup-native.sh|scripts/restore-drill-native.sh|scripts/restore-native.sh) ;;
   *) die "Неожиданная запись в архиве: $entry";;
  esac
 done < <(tar -tzf "$TMP/dist/$name")
 while IFS= read -r entry;do
  first="${entry:0:1}"
  [[ "$first" == '-' || "$first" == 'd' ]] || die 'В пакете запрещены симлинки и специальные файлы.'
 done < <(tar -tvzf "$TMP/dist/$name")
 tar -xzf "$TMP/dist/$name" -C "$TMP/dist"
 for entry in vacuumhub install.sh deploy/vacuumhub deploy/vacuumhub.service deploy/vacuumhub-cert-renew.service deploy/vacuumhub-cert-renew.timer scripts/backup-native.sh scripts/restore-drill-native.sh scripts/restore-native.sh;do
  [[ -f "$TMP/dist/$entry" ]] || die "В архиве отсутствует $entry."
 done
}
backup_old(){
 local path
 mkdir -p "$TMP/rollback"
 for path in "$BIN" "$MANAGER" "$SERVICE" "$ENV_FILE" "$INSTALL_DIR/install.sh" \
  "$INSTALL_DIR/scripts/backup-native.sh" "$INSTALL_DIR/scripts/restore-drill-native.sh" "$INSTALL_DIR/scripts/restore-native.sh" \
  /etc/systemd/system/vacuumhub-cert-renew.service /etc/systemd/system/vacuumhub-cert-renew.timer;do
  [[ ! -f "$path" ]] || { mkdir -p "$TMP/rollback$(dirname "$path")";cp -p "$path" "$TMP/rollback$path"; }
 done
 if [[ -d "$CERT_DIR" ]];then cp -a "$CERT_DIR" "$TMP/rollback-tls";fi
 WAS_ACTIVE=0;systemctl is-active --quiet vacuumhub.service && WAS_ACTIVE=1 || true
}
restore_old(){
 local path
 systemctl stop vacuumhub.service 2>/dev/null || true
 for path in "$BIN" "$MANAGER" "$SERVICE" "$ENV_FILE" "$INSTALL_DIR/install.sh" \
  "$INSTALL_DIR/scripts/backup-native.sh" "$INSTALL_DIR/scripts/restore-drill-native.sh" "$INSTALL_DIR/scripts/restore-native.sh" \
 /etc/systemd/system/vacuumhub-cert-renew.service /etc/systemd/system/vacuumhub-cert-renew.timer;do
  if [[ -f "$TMP/rollback$path" ]];then cp -p "$TMP/rollback$path" "$path"
  else rm -f "$path";fi
 done
 if [[ -d "$TMP/rollback-tls" ]];then
  rm -rf "$CERT_DIR";cp -a "$TMP/rollback-tls" "$CERT_DIR"
 fi
 systemctl daemon-reload || true
 if [[ "$WAS_ACTIVE" == 1 ]];then systemctl restart vacuumhub.service || true;fi
}
cleanup_install(){
 local rc=$?
 trap - EXIT
 if [[ "${CHANGED:-0}" == 1 && "${SUCCESS:-0}" != 1 ]];then
  echo 'Обновление не прошло, восстанавливается прежняя конфигурация/бинарник.' >&2
  restore_old
 fi
 [[ -z "${TMP:-}" ]] || rm -rf "$TMP"
 exit "$rc"
}
install_native(){
 local action="$1"
 check_system
 TMP="$(mktemp -d)"
 CHANGED=0;SUCCESS=0;WAS_ACTIVE=0
 trap cleanup_install EXIT
 prepare_directories
 if [[ "$action" == update || "$action" == install ]];then fetch_package;fi
 if [[ "$action" == update && -f "$ENV_FILE" ]];then
  ACCESS_MODE="$(get_config ACCESS_MODE)"
  PUBLIC_HOST="$(get_config PUBLIC_HOST)"
  LISTEN_PORT="$(get_config LISTEN_PORT)"
  TLS_MODE="$(get_config TLS_MODE)"
  CERT_NAME="$(get_config CERT_NAME)"
  ADMIN_BASIC_USER="$(get_config ADMIN_BASIC_USER)"
  PASSWORD_B64="$(get_config ADMIN_BASIC_PASSWORD_B64)"
  [[ "$ACCESS_MODE" == ip || "$ACCESS_MODE" == domain ]] || die 'Устаревший режим доступа; выберите настройку.'
 else
  config_access || return 0
  config_credentials
 fi
 backup_old
 # For routine upgrades, preserve certificates and access choices.
 if [[ "$action" != update || ! -f "$ENV_FILE" ]];then prepare_tls;fi
 write_environment "$TMP/environment"
 CHANGED=1
 if [[ "$action" == install || "$action" == update ]];then
  install -m 0755 "$TMP/dist/vacuumhub" "$BIN.new"
  mv -f "$BIN.new" "$BIN"
  install -m 0755 "$TMP/dist/install.sh" "$INSTALL_DIR/install.sh"
  install -m 0755 "$TMP/dist/deploy/vacuumhub" "$MANAGER"
  install -m 0644 "$TMP/dist/deploy/vacuumhub.service" "$SERVICE"
  install -d -m 0755 "$INSTALL_DIR/scripts"
  for script in backup-native.sh restore-drill-native.sh restore-native.sh;do
   install -m 0700 "$TMP/dist/scripts/$script" "$INSTALL_DIR/scripts/$script"
  done
  for unit in vacuumhub-cert-renew.service vacuumhub-cert-renew.timer;do
   install -m 0644 "$TMP/dist/deploy/$unit" "/etc/systemd/system/$unit"
  done
 fi
 install -m 0600 "$TMP/environment" "$ENV_FILE"
 systemctl daemon-reload
 systemctl enable vacuumhub.service >/dev/null
 if [[ "$TLS_MODE" == letsencrypt ]];then
  systemctl enable --now vacuumhub-cert-renew.timer >/dev/null
 else
  systemctl disable --now vacuumhub-cert-renew.timer >/dev/null 2>&1 || true
 fi
 systemctl restart vacuumhub.service
 local i=0
 until systemctl is-active --quiet vacuumhub.service;do
  i=$((i+1));((i<12)) || die 'Не удалось запустить службу. Проверьте journalctl -u vacuumhub.'
  sleep 1
 done
 sleep 1
 systemctl is-active --quiet vacuumhub.service || die 'Служба завершилась сразу после запуска.'
 SUCCESS=1
 CHANGED=0
 echo
 echo 'VacuumHub установлен без Docker и без локального режима.'
 local host="$PUBLIC_HOST"
 [[ "$host" != *:* ]] || host="[$host]"
 if [[ "$LISTEN_PORT" == 443 ]];then echo "Адрес: https://$host/"
 else echo "Адрес: https://$host:$LISTEN_PORT/";fi
 echo "Управление: sudo vacuumhub"
 [[ -z "${GENERATED_PASSWORD:-}" ]] || echo "Сгенерированный пароль (сохраните в защищённом месте): $GENERATED_PASSWORD"
 echo 'Если сайт недоступен, проверьте внешний firewall хостинга и TCP-порт панели.'
}
change_credentials(){
 [[ -f "$ENV_FILE" ]] || die 'Сначала установите VacuumHub.'
 check_system
 TMP="$(mktemp -d)";CHANGED=0;SUCCESS=0;WAS_ACTIVE=0;trap cleanup_install EXIT
 ACCESS_MODE="$(get_config ACCESS_MODE)"
 PUBLIC_HOST="$(get_config PUBLIC_HOST)"
 LISTEN_PORT="$(get_config LISTEN_PORT)"
 TLS_MODE="$(get_config TLS_MODE)"
 CERT_NAME="$(get_config CERT_NAME)"
 config_credentials
 backup_old
 write_environment "$TMP/environment"
 CHANGED=1
 install -m 0600 "$TMP/environment" "$ENV_FILE"
 systemctl restart vacuumhub.service
 systemctl is-active --quiet vacuumhub.service || die 'После смены пароля служба не запущена.'
 CHANGED=0;SUCCESS=1
 echo 'Логин и пароль обновлены.'
 [[ -z "${GENERATED_PASSWORD:-}" ]] || echo "Новый пароль: $GENERATED_PASSWORD"
}
copy_cert(){
 [[ -f "$ENV_FILE" ]] || die 'Служба не установлена.'
 PUBLIC_HOST="$(get_config PUBLIC_HOST)"
 CERT_NAME="$(get_config CERT_NAME)"
 [[ -n "$CERT_NAME" ]] || die 'Certbot не настроен.'
 install_cert_copy "$CONFIG_DIR/acme/live/$CERT_NAME/fullchain.pem" "$CONFIG_DIR/acme/live/$CERT_NAME/privkey.pem"
 systemctl restart vacuumhub.service
}
renew_cert(){
 [[ -f "$ENV_FILE" ]] || return 0
 [[ "$(get_config TLS_MODE)" == letsencrypt ]] || return 0
 command -v certbot >/dev/null || die 'Для продления установите Certbot.'
 certbot renew --quiet --non-interactive --config-dir /etc/vacuumhub/acme \
  --work-dir /var/lib/vacuumhub/acme --logs-dir /var/log/vacuumhub \
  --deploy-hook '/opt/vacuumhub/install.sh --copy-cert'
}
uninstall_native(){
 [[ -f "$ENV_FILE" || -f "$BIN" ]] || { echo 'VacuumHub не установлен.';return; }
 local choice=''
 if [[ "${VACUUMHUB_NONINTERACTIVE:-0}" != 1 ]];then
  read -r -p 'Удалить VacuumHub и его системную службу? Данные пока будут сохранены (y/n): ' choice </dev/tty || return
  [[ "$choice" == y || "$choice" == yes ]] || return
 else choice=y;fi
 systemctl disable --now vacuumhub.service vacuumhub-cert-renew.timer >/dev/null 2>&1 || true
 rm -f "$SERVICE" /etc/systemd/system/vacuumhub-cert-renew.service /etc/systemd/system/vacuumhub-cert-renew.timer "$MANAGER"
 rm -rf "$INSTALL_DIR"
 systemctl daemon-reload
 echo "Служба удалена. Данные сохранены в $STATE_DIR, конфигурация — в $CONFIG_DIR."
 if [[ "${VACUUMHUB_PURGE:-0}" == 1 ]];then
  if [[ "${VACUUMHUB_NONINTERACTIVE:-0}" != 1 ]];then
   read -r -p 'ВНИМАНИЕ: удалить все озвучки, прошивки и ключи без восстановления? Введите DELETE: ' choice </dev/tty || return
   [[ "$choice" == DELETE ]] || return
  fi
  rm -rf "$STATE_DIR" "$CONFIG_DIR"
  echo 'Все данные VacuumHub удалены.'
 fi
}
menu(){
 local option
 while true;do
  echo
  echo '════════ Установка VacuumHub ════════'
  echo '1) Установить / обновить (бинарник + systemd)'
  echo '2) Настроить подключение (IP или домен)'
  echo '3) Изменить логин и пароль'
  echo '4) Состояние службы'
  echo '5) Удалить'
  echo '0) Выход'
  read -r -p 'Выберите: ' option </dev/tty || return
  case "$option" in
   1) if [[ -f "$ENV_FILE" ]];then (install_native update);else (install_native install);fi;;
   2) [[ -f "$BIN" ]] || { echo 'Сначала установите VacuumHub.';continue; }
      (install_native configure);;
   3) (change_credentials);;
   4) systemctl --no-pager status vacuumhub.service || true;;
   5) (uninstall_native);;
   0) return;;
   *) echo 'Неизвестное действие.';;
  esac
 done
}
main(){
 require_root
 case "${1:-menu}" in
  menu) menu;;
  --install) (install_native install);;
  --update) (install_native update);;
  --configure) (install_native configure);;
  --credentials) (change_credentials);;
  --renew-cert) renew_cert;;
  --copy-cert) copy_cert;;
  --uninstall) (uninstall_native);;
  --status) systemctl status vacuumhub.service --no-pager;;
  *) echo 'Команды: --install --update --configure --credentials --renew-cert --uninstall --status' >&2;exit 2;;
 esac
}
if [[ -z "${BASH_SOURCE[0]:-}" || "${BASH_SOURCE[0]}" == "$0" ]];then main "$@";fi
