#!/bin/sh
# station-xiaozhi launcher: keeps the client running and restarts it after a failure.
# Used by the boot script and as the yam package entry point. A clean exit (SIGTERM) stops it.
here=$(cd "$(dirname "$0")" && pwd)
export STATION_XIAOZHI_HOME="${STATION_XIAOZHI_HOME:-/data/station-xiaozhi}"
export LD_LIBRARY_PATH="/system/vendor/quasar/libs:/system/vendor/lib:/system/lib:/usr/lib:/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
home=$STATION_XIAOZHI_HOME
log=$home/client.log
umask 077
mkdir -p "$home" /tmp/station-xiaozhi
# Package managers may unpack without the executable bit.
[ -x "$here/station-xiaozhi" ] || chmod 755 "$here/station-xiaozhi" 2>/dev/null

# One launcher at a time, whether started at boot, by the installer or by yam.
lock=/tmp/station-xiaozhi/run.pid
if [ -f "$lock" ] && other=$(cat "$lock") && [ "$other" != "$$" ] &&
    grep -q "run.sh" "/proc/$other/cmdline" 2>/dev/null; then
    echo "station-xiaozhi is already running (pid $other)."
    exit 0
fi
echo "$$" > "$lock"

# Under yam the log goes to `yam logs`; otherwise to client.log, trimmed to 256 KiB.
standalone=
[ -n "${YAM_PLUGIN_ID:-}" ] || standalone=1
delay=5
while [ -x "$here/station-xiaozhi" ]; do
    if [ -n "$standalone" ]; then
        size=$(wc -c < "$log" 2>/dev/null || echo 0)
        [ "$size" -le 524288 ] || { tail -c 262144 "$log" > "$log.tmp" && mv -f "$log.tmp" "$log"; }
        exec >> "$log" 2>&1
    fi
    started=$(cut -d. -f1 /proc/uptime)
    "$here/station-xiaozhi" run < /dev/null
    status=$?
    [ "$status" = 0 ] && break
    # A client that ran for a while restarts quickly; a crash loop backs off up to 30 s.
    [ $(( $(cut -d. -f1 /proc/uptime) - started )) -le 60 ] || delay=5
    echo "station-xiaozhi exited with status $status; restarting in $delay s."
    sleep "$delay"
    delay=$(( delay * 2 ))
    [ "$delay" -le 30 ] || delay=30
done
rm -f "$lock"
