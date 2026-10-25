#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

case "$PWD" in *[[:space:]]*)
	echo 'Build path must not contain whitespace (Opus/libtool limitation).' >&2
	exit 2
	;;
esac

GO=${GO:-go}
UV=${UV:-uv}
PYTHON=${PYTHON:-python3}
JOBS=${JOBS:-2}
MAKE=${MAKE:-make}
# Optional: a local Zig 0.17.0 executable instead of the uv-managed one (e.g. on Windows),
# and directories to put first on PATH (e.g. a POSIX sh without spaces in its path for make).
ZIG=${ZIG:-}
[ -z "${EXTRA_PATH:-}" ] || export PATH="$EXTRA_PATH:$PATH"
case "${1:-}" in setup | build) action=$1 ;; *)
	echo 'Usage: sh tools/build-arm.sh setup|build' >&2
	exit 2
	;;
esac
case "$JOBS" in '' | *[!0-9]*)
	echo 'JOBS must be an integer from 1 to 32.' >&2
	exit 2
	;;
esac
[ "$JOBS" -ge 1 ] && [ "$JOBS" -le 32 ] || {
	echo 'JOBS must be from 1 to 32.' >&2
	exit 2
}
zig_tool=$UV
[ -z "$ZIG" ] || zig_tool=$ZIG
for tool in "$GO" "$zig_tool" "$PYTHON" "$MAKE" pkg-config curl tar; do
	command -v "$tool" >/dev/null || {
		echo "Required command missing: $tool" >&2
		exit 1
	}
done

cache="$PWD/.cache/arm-opus"
prefix="$cache/install"
archive="$cache/opus-1.6.1.tar.gz"
sha=6ffcb593207be92584df15b32466ed64bbec99109f007c82205f0194572411a1
mkdir -p "$cache"
work=$(mktemp -d "$cache/work.XXXXXX")
output=
trap 'rm -rf "$work"; if [ -n "$output" ]; then rm -f "$output"; fi' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for compiler in cc ar ranlib; do
	wrapper="$PWD/.cache/arm-$compiler"
	case "$compiler" in cc) flags=' -target arm-linux-gnueabihf.2.30 -mcpu=cortex_a7' ;; *) flags= ;; esac
	if [ -n "$ZIG" ]; then
		printf '#!/bin/sh\nexec "%s" %s%s "$@"\n' "$ZIG" "$compiler" "$flags" >"$wrapper"
	else
		printf '#!/bin/sh\nexec "${UV:-uv}" tool run --from ziglang==0.17.0 python-zig %s%s "$@"\n' "$compiler" "$flags" >"$wrapper"
	fi
	chmod 755 "$wrapper"
done
export UV
if [ -n "$ZIG" ]; then zig_version=$("$ZIG" version); else zig_version=$("$UV" tool run --from ziglang==0.17.0 python-zig version); fi
[ "$zig_version" = 0.17.0 ] || {
	echo 'Unexpected Zig version.' >&2
	exit 1
}

# The absolute prefix is embedded in opus.pc; a moved cache must be rebuilt.
key="zig=0.17.0${ZIG:+-local} target=arm-linux-gnueabihf.2.30 cpu=cortex_a7 opus=$sha static,no-extra,no-doc,no-intrinsics,no-rtcd,no-asm prefix=$prefix"
if [ ! -f "$prefix/.build-key" ] || [ "$(cat "$prefix/.build-key")" != "$key" ] ||
	[ ! -f "$prefix/lib/libopus.a" ] || [ ! -f "$prefix/include/opus/opus.h" ] || [ ! -f "$prefix/lib/pkgconfig/opus.pc" ]; then
	if [ ! -f "$archive" ]; then
		curl --fail --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
			--output "$work/opus.tar.gz" https://downloads.xiph.org/releases/opus/opus-1.6.1.tar.gz
		mv "$work/opus.tar.gz" "$archive"
	fi
	"$PYTHON" -c 'import hashlib, pathlib, sys; actual = hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest(); sys.exit(0 if actual == sys.argv[2] else "Opus archive checksum mismatch; remove the archive and rerun setup.")' "$archive" "$sha"
	tar -xzf "$archive" -C "$work"
	rm -rf "$prefix"
	(
		cd "$work/opus-1.6.1"
		CC="$OLDPWD/.cache/arm-cc" AR="$OLDPWD/.cache/arm-ar" RANLIB="$OLDPWD/.cache/arm-ranlib" \
			./configure --host=arm-linux-gnueabihf --prefix="$prefix" \
			--disable-shared --enable-static --disable-extra-programs --disable-doc \
			--disable-intrinsics --disable-rtcd --disable-asm
		"$MAKE" -j "$JOBS"
		"$MAKE" install
	)
	printf '%s\n' "$key" >"$prefix/.build-key"
fi

export PKG_CONFIG_LIBDIR="$prefix/lib/pkgconfig" PKG_CONFIG_PATH= PKG_CONFIG_SYSROOT_DIR=
[ "$(pkg-config --modversion opus)" = 1.6.1 ] || {
	echo 'ARM Opus cache version mismatch.' >&2
	exit 1
}
if [ "$action" = build ]; then
	mkdir -p build
	output=$(mktemp "$PWD/build/.station-xiaozhi.XXXXXX")
	# Go on Windows cannot execute the shell-script wrapper, so a local Zig is called directly.
	cc="$PWD/.cache/arm-cc"
	[ -z "$ZIG" ] || cc="$ZIG cc -target arm-linux-gnueabihf.2.30 -mcpu=cortex_a7"
	# CGO_CFLAGS replaces Go's default -O2 -g, so restore optimization explicitly. The prefix maps
	# keep the builder's directories out of C file names embedded in the binary (in every spelling
	# a Windows build produces).
	cflags="-O2 -g0"
	for dir in "$("$GO" env GOMODCACHE)" "$PWD" "$(pwd -W 2>/dev/null || true)" ${ZIG:+"$(dirname "$ZIG")"}; do
		[ -n "$dir" ] || continue
		for spelling in "$dir" "$(printf '%s' "$dir" | tr '\\' /)" "$(printf '%s' "$dir" | tr / '\\')"; do
			cflags="$cflags -ffile-prefix-map=$spelling=/build"
		done
	done
	CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 CC="$cc" \
		CGO_CFLAGS="$cflags $(pkg-config --cflags opus)" CGO_LDFLAGS="$(pkg-config --static --libs opus)" \
		"$GO" build -mod=readonly -tags=nolibopusfile -trimpath -buildvcs=false \
		-ldflags='-s -w' -o "$output" ./cmd/station-xiaozhi
	mv -f "$output" build/station-xiaozhi
	output=
	printf '%s\n' 'Built build/station-xiaozhi for Linux ARMv7.'
fi
