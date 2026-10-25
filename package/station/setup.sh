#!/bin/sh
# station-xiaozhi setup, run on the Station as root by install.sh / install.ps1:
#   sh setup.sh install | uninstall | start | stop | status | bind
set -u
src=$(cd "$(dirname "$0")" && pwd)
app=/data/station-xiaozhi
init=/etc/init.d/S99station-xiaozhi
# Earlier builds of this assistant (ChatGPT era); their binding identity is carried over.
legacy=/data/yandex-station-chatgpt-voice
legacy_init=/etc/init.d/S99zchatgpt-voice

die() {
    echo "Ошибка: $*" >&2
    exit 1
}

# signal_matching PATTERN: send TERM to every process whose command line contains PATTERN.
signal_matching() {
    for d in /proc/[0-9]*; do
        pid=${d#/proc/}
        [ "$pid" = "$$" ] && continue
        cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
        case "$cmd" in *"$1"*) kill -TERM "$pid" 2>/dev/null ;; esac
    done
}

running() {
    for d in /proc/[0-9]*; do
        cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
        case "$cmd" in *"$app/run.sh"* | *"$app/station-xiaozhi run"*) return 0 ;; esac
    done
    return 1
}

stop_all() {
    signal_matching "$app/run.sh"
    signal_matching "$app/station-xiaozhi run"
    signal_matching "$legacy/supervise.sh"
    signal_matching "$legacy/client run"
    tries=0
    while running && [ "$tries" -lt 10 ]; do
        sleep 1
        tries=$((tries + 1))
    done
}

start() {
    touch "$app/enabled"
    setsid sh "$app/run.sh" < /dev/null > /dev/null 2>&1 &
    sleep 3
    running || die "ассистент не запустился; смотрите $app/client.log"
}

# with_rootfs COMMAND...: run with the read-only system partition briefly writable.
with_rootfs() {
    mount -o remount,rw / || die "не удалось открыть системный раздел на запись"
    "$@"
    status=$?
    sync
    mount -o remount,ro /
    return "$status"
}

install_init() {
    cp "$src/S99station-xiaozhi" "$init" && chmod 755 "$init" && rm -f "$legacy_init"
}

remove_init() {
    rm -f "$init" "$legacy_init"
}

client() {
    STATION_XIAOZHI_HOME=$app \
        LD_LIBRARY_PATH=/system/vendor/quasar/libs:/system/vendor/lib:/system/lib:/usr/lib:/lib \
        "$app/station-xiaozhi" "$@"
}

[ "$(id -u)" = 0 ] || die "нужен root (сетевой ADB с правами root)"

case "${1:-}" in
install)
    [ -x /system/vendor/quasar/maind ] && [ -d /system/vendor/quasar/spotter_models/activation/yandex ] ||
        die "это не похоже на Яндекс Станцию с рабочей Алисой"
    [ -f "$src/station-xiaozhi" ] || die "в пакете нет программы station-xiaozhi"
    echo "Останавливаю прежнюю версию..."
    stop_all
    mkdir -p "$app" && chmod 700 "$app" || die "не удалось создать $app"
    if [ ! -f "$app/xiaozhi.json" ] && [ -f "$legacy/xiaozhi.json" ]; then
        cp "$legacy/xiaozhi.json" "$app/xiaozhi.json" && echo "Привязка к xiaozhi.me перенесена из прежней версии."
    fi
    cp "$src/station-xiaozhi" "$app/station-xiaozhi.new" && chmod 700 "$app/station-xiaozhi.new" &&
        mv -f "$app/station-xiaozhi.new" "$app/station-xiaozhi" || die "не удалось скопировать программу"
    cp "$src/run.sh" "$app/run.sh" && chmod 700 "$app/run.sh" || die "не удалось скопировать run.sh"
    with_rootfs install_init || die "не удалось установить автозапуск"
    rm -rf "$legacy" /data/.yandex-station-chatgpt-voice.lock
    echo "Запускаю ассистента..."
    start
    echo "Установлено: station-xiaozhi $(client version)."
    ;;
uninstall)
    stop_all
    with_rootfs remove_init
    rm -rf "$app" "$legacy" /tmp/station-xiaozhi
    echo "station-xiaozhi удалён. Станция работает как раньше; привязку на xiaozhi.me можно удалить в личном кабинете."
    ;;
start)
    running && { echo "Уже запущен."; exit 0; }
    start
    echo "Запущен."
    ;;
stop)
    rm -f "$app/enabled"
    stop_all
    echo "Остановлен и не будет запускаться при загрузке (включить снова: start)."
    ;;
status)
    if running; then echo "Работает."; else echo "Не запущен."; fi
    [ -f "$app/enabled" ] && echo "Автозапуск включён." || echo "Автозапуск выключен."
    [ -x "$app/station-xiaozhi" ] && echo "Версия: $(client version)"
    echo "Последние строки журнала:"
    tail -n 15 "$app/client.log" 2>/dev/null
    ;;
bind)
    client bind
    ;;
*)
    die "использование: sh setup.sh install|uninstall|start|stop|status|bind"
    ;;
esac
