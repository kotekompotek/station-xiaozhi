package voice

import (
	"os"
	"path/filepath"
)

const Version = "1.0.0"

// homeDir holds state that must survive reinstalls and package updates: the binding identity and logs.
func homeDir() string {
	if home := os.Getenv("STATION_XIAOZHI_HOME"); home != "" {
		return home
	}
	return "/data/station-xiaozhi"
}

// runtimeDir holds diagnostics that may vanish on reboot.
const runtimeDir = "/tmp/station-xiaozhi"

func identityPath() string { return filepath.Join(homeDir(), "xiaozhi.json") }

// Raw events and microphone audio are dumped only while this marker file exists.
func debugMarker() string { return filepath.Join(homeDir(), "debug") }
