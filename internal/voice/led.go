package voice

import (
	"context"
	"math"
	"os"
	"strconv"
	"time"
)

const ringFirmwareTrigger = "led_ring_fb"
const breathPeriod = 3 * time.Second
const breathStep = 40 * time.Millisecond

// Kept well below 255: the ring shares the supply with the amplifier.
const breathMin, breathMax = 8.0, 150.0

var ringLEDs = func() []string {
	var leds []string
	for _, color := range []string{"red", "green", "blue"} {
		for _, n := range []string{"1", "2", "3"} {
			leds = append(leds, "/sys/class/leds/plum:"+color+":ring"+n)
		}
	}
	return leds
}()

// breatheRing pulses the ring white until ctx ends, then hands it back to the firmware animator.
func breatheRing(ctx context.Context) {
	for _, led := range ringLEDs {
		if os.WriteFile(led+"/trigger", []byte("none"), 0) != nil {
			restoreRing()
			return
		}
	}
	defer restoreRing()
	ticker := time.NewTicker(breathStep)
	defer ticker.Stop()
	start := time.Now()
	for {
		phase := 2 * math.Pi * float64(time.Since(start)) / float64(breathPeriod)
		wave := (1 - math.Cos(phase)) / 2
		level := []byte(strconv.Itoa(int(breathMin + (breathMax-breathMin)*wave*wave)))
		for _, led := range ringLEDs {
			_ = os.WriteFile(led+"/brightness", level, 0)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func restoreRing() {
	for _, led := range ringLEDs {
		_ = os.WriteFile(led+"/brightness", []byte("0"), 0)
		_ = os.WriteFile(led+"/trigger", []byte(ringFirmwareTrigger), 0)
	}
}
