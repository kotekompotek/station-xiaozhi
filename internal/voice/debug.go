package voice

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const dumpLimit = 4 << 20

func openDump(name string) *os.File {
	if _, err := os.Stat(debugMarker()); err != nil {
		return nil
	}
	if os.MkdirAll(runtimeDir, 0o700) != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(runtimeDir, name), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	if info, err := f.Stat(); err != nil || info.Size() > dumpLimit {
		f.Close()
		return nil
	}
	return f
}

// dumpUplink appends raw 16 kHz mono capture before gain, for level tuning.
func dumpUplink(pcm []int16) {
	if f := openDump("uplink.pcm"); f != nil {
		defer f.Close()
		_ = binary.Write(f, binary.LittleEndian, pcm)
	}
}

func dumpEvent(data []byte) {
	if f := openDump("events.log"); f != nil {
		defer f.Close()
		fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05.000"), data)
	}
}
