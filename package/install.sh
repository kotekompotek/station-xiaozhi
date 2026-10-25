#!/bin/sh
# Installs station-xiaozhi on a rooted Yandex Station over network ADB.
#   sh install.sh [STATION_IP]          install or update
#   sh install.sh [STATION_IP] --uninstall
#   sh install.sh [STATION_IP] status | start | stop | bind
set -eu
cd "$(dirname "$0")"
export MSYS_NO_PATHCONV=1 # Git Bash on Windows must not rewrite Station paths.
ADB=${ADB:-adb}
remote=/data/local/tmp/station-xiaozhi-setup

die() {
    echo "Ошибка: $*" >&2
    exit 1
}

command -v "$ADB" > /dev/null 2>&1 ||
    die "не найден adb. Установите Android platform-tools: https://developer.android.com/tools/releases/platform-tools"

target= action=install
for arg in "$@"; do
    case "$arg" in
    --uninstall | uninstall) action=uninstall ;;
    status | start | stop | bind) action=$arg ;;
    -h | --help)
        sed -n '2,5p' "$0"
        exit 0
        ;;
    *) target=$arg ;;
    esac
done

if [ -n "$target" ]; then
    case "$target" in *:*) ;; *) target=$target:5555 ;; esac
    "$ADB" connect "$target" > /dev/null 2>&1 || true
    serial=$target
else
    serial=$("$ADB" devices | awk 'NR > 1 && $2 == "device" { print $1; exit }')
    [ -n "$serial" ] || die "станция не найдена. Укажите её IP: sh install.sh 192.168.0.125"
fi
adb_station() { "$ADB" -s "$serial" "$@"; }

state=$(adb_station get-state 2> /dev/null || true)
[ "$state" = device ] || die "нет связи со станцией $serial по ADB (состояние: ${state:-нет ответа})"
[ "$(adb_station shell id -u | tr -d '\r')" = 0 ] || die "на станции нет root-доступа через ADB"
echo "Станция: $serial"

adb_station shell "rm -rf $remote && mkdir -p $remote"
files="station/setup.sh"
[ "$action" = install ] && files="station-xiaozhi station/run.sh station/S99station-xiaozhi station/setup.sh"
for file in $files; do
    [ -f "$file" ] || die "в папке установщика нет файла $file"
    adb_station push "$file" "$remote/" > /dev/null || die "не удалось передать $file на станцию"
done

status=0
adb_station shell "sh $remote/setup.sh $action" || status=$?
if [ "$action" = install ] && [ "$status" = 0 ]; then
    echo
    echo "Проверяю привязку к xiaozhi.me..."
    adb_station shell "sh $remote/setup.sh bind" || status=$?
fi
adb_station shell "rm -rf $remote" || true
[ "$status" = 0 ] || exit "$status"
if [ "$action" = install ]; then
    echo
    echo "Готово. Скажите станции «Яндекс» и задайте вопрос. «Алиса» работает как обычно."
fi
