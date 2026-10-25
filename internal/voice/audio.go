package voice

// #include <stdlib.h>
import "C"

import (
	"bytes"
	"context"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/hraban/opus"
)

const captureSamples = 320
const samplesPerFrame = 960

// XiaoZhi uplink: 60 ms of 16 kHz mono, three processed capture frames.
const uplinkFrameSamples = 3 * captureSamples

// The server sends speech in bursts; buffer up to 3 s of 20 ms blocks.
const playbackBlocks = 150

type captureFrame struct {
	epoch, sequence uint64
	pcm             [captureSamples]int16
}

type playbackBlock struct {
	epoch uint64
	used  int
	pcm   [samplesPerFrame * 2]int16
}

type audio struct {
	epoch         *stationEpoch
	mu            sync.Mutex
	native        *malgo.Device
	nativeContext *malgo.AllocatedContext
	playback      chan playbackBlock
	playbackDrops atomic.Uint64
	heard         atomic.Int64
	sentPackets   atomic.Int64
	// Sum over sent packets of their mean absolute sample level.
	sentLevel atomic.Int64
	stopping  atomic.Bool
	current   playbackBlock
	currentAt int
}

func newAudio(epoch *stationEpoch) *audio {
	return &audio{epoch: epoch, playback: make(chan playbackBlock, playbackBlocks)}
}

func (a *audio) open() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.epoch.allowed() {
		return context.Cause(a.epoch.ctx)
	}
	c, err := malgo.InitContext([]malgo.Backend{malgo.BackendAlsa}, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("%w: cannot initialize ALSA playback: %w", errStationAudio, err)
	}
	a.nativeContext = c
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 2
	cfg.SampleRate = 48000
	cfg.PeriodSizeInFrames = samplesPerFrame
	var id malgo.DeviceID
	copy(id[:], "default")
	cfg.Playback.DeviceID = id.Pointer()
	defer C.free(cfg.Playback.DeviceID)
	a.native, err = malgo.InitDevice(c.Context, cfg, malgo.DeviceCallbacks{Data: a.callback, Stop: func() {
		if !a.stopping.Load() {
			a.epoch.revoke(fmt.Errorf("%w: playback device stopped", errStationAudio))
		}
	}})
	if err != nil {
		return fmt.Errorf("%w: cannot open ALSA default PCM: %w", errStationAudio, err)
	}
	if !a.epoch.allowed() {
		return context.Cause(a.epoch.ctx)
	}
	if err := a.native.Start(); err != nil {
		return fmt.Errorf("%w: cannot start playback: %w", errStationAudio, err)
	}
	return nil
}

func (a *audio) close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopping.Store(true)
	var err error
	if a.native != nil {
		err = a.native.Stop()
		a.native.Uninit()
		a.native = nil
	}
	if a.nativeContext != nil {
		err = errors.Join(err, a.nativeContext.Uninit())
		a.nativeContext.Free()
		a.nativeContext = nil
	}
	if err != nil {
		return fmt.Errorf("%w: ALSA playback did not close cleanly: %w", errStationAudio, err)
	}
	return nil
}

func (a *audio) callback(output, _ []byte, frames uint32) {
	clear(output)
	if !a.epoch.allowed() {
		return
	}
	for i := 0; i < min(int(frames), len(output)/4); i++ {
		if !a.epoch.allowed() {
			clear(output)
			return
		}
		for attempt := 0; a.currentAt >= a.current.used && attempt < cap(a.playback); attempt++ {
			select {
			case block := <-a.playback:
				if block.epoch != a.epoch.number {
					continue
				}
				a.current = block
				a.currentAt = 0
			default:
				attempt = cap(a.playback)
			}
		}
		if a.currentAt >= a.current.used {
			continue
		}
		left, right := a.current.pcm[a.currentAt*2], a.current.pcm[a.currentAt*2+1]
		binary.LittleEndian.PutUint16(output[i*4:], uint16(left))
		binary.LittleEndian.PutUint16(output[i*4+2:], uint16(right))
		a.currentAt++
	}
	if !a.epoch.allowed() {
		clear(output)
		return
	}
}

// send encodes 60 ms Opus packets from processed capture; muted packets are read but not sent.
func (a *audio) send(ctx context.Context, input <-chan captureFrame, muted func() bool, transmit func([]byte) error) error {
	encoder, err := opus.NewEncoder(16000, 1, opus.AppVoIP)
	if err != nil {
		return fmt.Errorf("%w: cannot initialize Opus encoder", errStationAudio)
	}
	if encoder.SetBitrate(24000) != nil {
		return fmt.Errorf("%w: cannot configure Opus encoder", errStationAudio)
	}
	var packet [4000]byte
	var pcm [uplinkFrameSamples]int16
	var gain uplinkGain
	filled := 0
	var expected uint64
	haveFrame := false
	for {
		var frame captureFrame
		select {
		case <-ctx.Done():
			return nil
		case next, ok := <-input:
			if !ok {
				return nil
			}
			frame = next
		}
		if frame.epoch != a.epoch.number || !a.epoch.allowed() {
			return nil
		}
		if haveFrame && frame.sequence != expected {
			return fmt.Errorf("%w: processed capture discontinuity", errStationAudio)
		}
		expected = frame.sequence + 1
		haveFrame = true
		filled += copy(pcm[filled:], frame.pcm[:])
		if filled < len(pcm) {
			continue
		}
		filled = 0
		if muted() {
			continue
		}
		var level int64
		for _, sample := range pcm {
			level += max(int64(sample), -int64(sample))
		}
		a.sentPackets.Add(1)
		a.sentLevel.Add(level / int64(len(pcm)))
		dumpUplink(pcm[:])
		gain.apply(pcm[:])
		n, err := encoder.Encode(pcm[:], packet[:])
		if err != nil {
			return fmt.Errorf("%w: Opus capture encoding failed", errStationAudio)
		}
		if !a.epoch.allowed() {
			return nil
		}
		if err := transmit(packet[:n]); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errCallDropped
		}
	}
}

// The processed capture is tuned for the wake-word spotter and is far too quiet
// for the assistant's speech detector, so the uplink is normalized.
const (
	uplinkTargetPeak = 12000.0
	maxUplinkGain    = 30.0
	// Per 60 ms packet: the envelope halves in about 1.4 s, the gain recovers in about 1 s.
	envelopeRelease = 0.97
	gainRecovery    = 0.06
)

// uplinkGain is an automatic gain control with instant attack and slow release.
type uplinkGain struct {
	envelope, gain float64
}

func (g *uplinkGain) apply(pcm []int16) {
	var peak float64
	for _, sample := range pcm {
		peak = max(peak, math.Abs(float64(sample)))
	}
	g.envelope = max(peak, g.envelope*envelopeRelease)
	target := min(maxUplinkGain, uplinkTargetPeak/max(g.envelope, 1))
	if g.gain == 0 || target < g.gain {
		g.gain = target
	} else {
		g.gain += (target - g.gain) * gainRecovery
	}
	for i, sample := range pcm {
		pcm[i] = int16(max(math.MinInt16, min(math.MaxInt16, float64(sample)*g.gain)))
	}
}

// decoder turns server Opus packets (any rate, mono) into 48 kHz stereo playback.
type decoder struct {
	opus *opus.Decoder
	pcm  []int16
}

func newDecoder() (*decoder, error) {
	d, err := opus.NewDecoder(48000, 2)
	if err != nil {
		return nil, errors.New("cannot initialize Opus decoder")
	}
	return &decoder{opus: d, pcm: make([]int16, 5760*2)}, nil
}

func (a *audio) play(d *decoder, packet []byte) error {
	n, err := d.opus.Decode(packet, d.pcm)
	if err != nil {
		return errors.New("invalid server Opus packet")
	}
	a.queueDecoded(d.pcm[:n*2])
	return nil
}

// Mean absolute sample level above which remote audio counts as speech.
const audibleLevel = 300

func (a *audio) queueDecoded(pcm []int16) {
	if !a.epoch.allowed() {
		return
	}
	var level int64
	for _, sample := range pcm {
		level += max(int64(sample), -int64(sample))
	}
	if len(pcm) > 0 && level/int64(len(pcm)) > audibleLevel {
		a.heard.Store(time.Now().UnixNano())
	}
	for len(pcm) > 0 {
		n := min(len(pcm), samplesPerFrame*2)
		block := playbackBlock{epoch: a.epoch.number, used: n / 2}
		copy(block.pcm[:], pcm[:n])
		pcm = pcm[n:]
		select {
		case a.playback <- block:
		default:
			a.playbackDrops.Add(1)
		}
	}
}

// Call start and end sounds, decoded to raw S16LE 48 kHz stereo.
//
//go:embed cues/connected.pcm
var connectedCue []byte

//go:embed cues/hangup.pcm
var hangupCue []byte

// Russian ringback: 425 Hz, 0.8 s on, 3.2 s off.
//
//go:embed cues/ringback.pcm
var ringbackCue []byte

// Spoken failure reasons (Windows "Irina" voice).
var (
	//go:embed cues/error_connect.pcm
	connectFailedCue []byte
	//go:embed cues/error_dropped.pcm
	callDroppedCue []byte
	//go:embed cues/bind_intro.pcm
	bindIntroCue []byte
	//go:embed cues/bind_outro.pcm
	bindOutroCue []byte
	//go:embed cues/digits
	digitCues embed.FS
)

const bytesPerSecond = 48000 * 2 * 2

func playReadyCue(ctx context.Context) error {
	return playCue(ctx, connectedCue)
}

func playEndCue(ctx context.Context) error {
	return playCue(ctx, hangupCue)
}

// bindingSpeech reads the binding code aloud digit by digit, so a Station can be set up without a computer.
func bindingSpeech(code string) []byte {
	pause := make([]byte, bytesPerSecond/4)
	speech := append(append([]byte{}, bindIntroCue...), pause...)
	for _, digit := range code {
		if cue, err := digitCues.ReadFile("cues/digits/" + string(digit) + ".pcm"); err == nil {
			speech = append(append(speech, cue...), pause...)
		}
	}
	return append(append(speech, pause...), bindOutroCue...)
}

// failureCue picks the spoken explanation for a failed voice call.
func failureCue(err error) []byte {
	var unbound activationError
	switch {
	case errors.As(err, &unbound):
		return bindingSpeech(unbound.code)
	case errors.Is(err, errConnectFailed):
		return connectFailedCue
	}
	return callDroppedCue
}

type loopReader struct {
	data []byte
	at   int
}

func (r *loopReader) Read(p []byte) (int, error) {
	n := copy(p, r.data[r.at:])
	r.at = (r.at + n) % len(r.data)
	return n, nil
}

// playRingback repeats the ringback tone until ctx ends.
func playRingback(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "/usr/bin/aplay", "-q", "-D", "default", "-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "2")
	cmd.Stdin = &loopReader{data: ringbackCue}
	_ = cmd.Run()
}

func playCue(parent context.Context, data []byte) error {
	ctx, cancel := context.WithTimeout(parent, time.Duration(len(data))*time.Second/bytesPerSecond+3*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/aplay", "-q", "-D", "default", "-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "2")
	cmd.Stdin = bytes.NewReader(data)
	if err := cmd.Run(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return fmt.Errorf("cannot play voice cue: %w", err)
	}
	return nil
}
