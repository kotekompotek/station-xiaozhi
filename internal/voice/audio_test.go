package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/hraban/opus"
)

func toneFrame(epoch uint64, frame int, hz float64) captureFrame {
	f := captureFrame{epoch: epoch, sequence: uint64(frame)}
	for i := range f.pcm {
		f.pcm[i] = int16(12000 * math.Sin(2*math.Pi*hz*float64(frame*captureSamples+i)/16000))
	}
	return f
}

func tonePower(pcm []byte, hz float64) float64 {
	var sine, cosine float64
	for i := range len(pcm) / 4 {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[i*4:])))
		angle := 2 * math.Pi * hz * float64(i) / 48000
		sine += sample * math.Sin(angle)
		cosine += sample * math.Cos(angle)
	}
	return sine*sine + cosine*cosine
}

func goodTone(pcm []byte, want, other float64) bool {
	var energy float64
	for i := 0; i < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		energy += v * v
	}
	return energy/float64(len(pcm)/2) > 1e6 && tonePower(pcm, want) > 10*tonePower(pcm, other)
}

func allZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

func TestPlaybackBoundsAndRevocation(t *testing.T) {
	e := newStationEpoch(t.Context(), 1)
	defer e.revoke(context.Canceled)
	a := e.output
	pcm := make([]int16, samplesPerFrame*2)
	for i := range pcm {
		pcm[i] = 1000
	}
	for range playbackBlocks + 2 {
		a.queueDecoded(pcm)
	}
	if len(a.playback) != playbackBlocks || a.playbackDrops.Load() != 2 {
		t.Fatal("playback queue is not bounded")
	}
	output := make([]byte, 480*4)
	a.callback(output, nil, 480)
	if allZero(output) {
		t.Fatal("consumed PCM produced no output")
	}
	e.revoke(errAlicePriority)
	a.queueDecoded(pcm)
	a.callback(output, nil, 480)
	if !allZero(output) {
		t.Fatal("revoked current or queued PCM escaped")
	}
}

func TestSendEncodes60msPacketsAndHonorsMute(t *testing.T) {
	e := newStationEpoch(t.Context(), 1)
	defer e.revoke(context.Canceled)
	input := make(chan captureFrame, 12)
	for i := range 12 {
		input <- toneFrame(1, i, 440)
	}
	close(input)
	var packets [][]byte
	sent := 0
	err := e.output.send(t.Context(), input, func() bool { sent++; return sent == 2 }, func(p []byte) error {
		packets = append(packets, slices.Clone(p))
		return nil
	})
	if err != nil || len(packets) != 3 {
		t.Fatalf("12 frames should give 4 packets, one muted: %d %v", len(packets), err)
	}
	dec, err := opus.NewDecoder(16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 5760)
	if n, err := dec.Decode(packets[0], pcm); err != nil || n != uplinkFrameSamples {
		t.Fatal("packet is not 60 ms of 16 kHz mono", n, err)
	}
}

func TestPlayDecodesServerOpusToStereo48k(t *testing.T) {
	e := newStationEpoch(t.Context(), 1)
	defer e.revoke(context.Canceled)
	enc, err := opus.NewEncoder(24000, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]int16, 1440)
	for i := range frame {
		frame[i] = int16(12000 * math.Sin(2*math.Pi*440*float64(i)/24000))
	}
	packet := make([]byte, 4000)
	n, err := enc.Encode(frame, packet)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := newDecoder()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.output.play(dec, packet[:n]); err != nil {
		t.Fatal(err)
	}
	if len(e.output.playback) != 3 {
		t.Fatal("60 ms of server audio is not three 20 ms playback blocks", len(e.output.playback))
	}
	if e.output.play(dec, []byte{0xff, 0xff, 0xff}) == nil {
		t.Fatal("corrupt packet accepted")
	}
}

func TestPlaybackSetupCancellationIsNotANativeFault(t *testing.T) {
	e := newStationEpoch(t.Context(), 1)
	timeout := errors.New("voice connection setup timed out")
	e.revoke(timeout)
	if err := e.output.open(); !errors.Is(err, timeout) || errors.Is(err, errStationAudio) {
		t.Fatal("setup cancellation became a fatal native audio error", err)
	}
}

func TestEmbeddedCuesAreShortStereoPCM(t *testing.T) {
	for name, data := range map[string][]byte{
		"connected": connectedCue, "hangup": hangupCue, "ringback": ringbackCue,
		"connect": connectFailedCue, "dropped": callDroppedCue, "intro": bindIntroCue, "outro": bindOutroCue,
	} {
		t.Run(name, func(t *testing.T) {
			if len(data)%4 != 0 || len(data) < 48000*4/2 || len(data) > 48000*4*7 {
				t.Fatal("cue is not 0.5-7 s of stereo 48 kHz S16 PCM", len(data))
			}
			for i := 0; i < len(data); i += 2 {
				if sample := int16(binary.LittleEndian.Uint16(data[i:])); sample > 16384 || sample < -16384 {
					t.Fatal("cue exceeds 0.5 volume")
				}
			}
		})
	}
}

func TestFailureCueMatchesReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want []byte
	}{
		{fmt.Errorf("%w: dial tcp: i/o timeout", errConnectFailed), connectFailedCue},
		{errCallDropped, callDroppedCue},
		{errors.New("invalid server Opus packet"), callDroppedCue},
	} {
		if got := failureCue(voiceCallError{tc.err}); &got[0] != &tc.want[0] {
			t.Errorf("wrong cue for %v", tc.err)
		}
	}
	speech := failureCue(voiceCallError{activationError{"123456"}})
	digits := 0
	for _, d := range "123456" {
		cue, err := digitCues.ReadFile("cues/digits/" + string(d) + ".pcm")
		if err != nil {
			t.Fatal(err)
		}
		digits += len(cue)
	}
	if !bytes.HasPrefix(speech, bindIntroCue) || !bytes.HasSuffix(speech, bindOutroCue) ||
		len(speech) < len(bindIntroCue)+len(bindOutroCue)+digits || len(speech)%4 != 0 {
		t.Fatal("binding speech does not read the code between intro and outro")
	}
}

func TestLoopReaderRepeats(t *testing.T) {
	r := &loopReader{data: []byte("abc")}
	got := make([]byte, 7)
	for n := 0; n < len(got); {
		read, _ := r.Read(got[n:])
		n += read
	}
	if string(got) != "abcabca" {
		t.Fatalf("loop read %q", got)
	}
}

func TestCanceledCompletionDoesNotStartPlayback(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errAlicePriority)
	if err := playEndCue(ctx); !errors.Is(err, errAlicePriority) {
		t.Fatal("canceled completion attempted playback", err)
	}
}

func TestUplinkGainLiftsQuietSpeechWithoutClipping(t *testing.T) {
	var g uplinkGain
	quiet := make([]int16, uplinkFrameSamples)
	for i := range quiet {
		quiet[i] = int16(200 * math.Sin(2*math.Pi*300*float64(i)/16000))
	}
	g.apply(quiet)
	peak := 0
	for _, s := range quiet {
		peak = max(peak, int(s), -int(s))
	}
	if peak < 5000 || peak > 13000 {
		t.Fatal("quiet speech not normalized", peak)
	}
	loud := make([]int16, uplinkFrameSamples)
	for i := range loud {
		loud[i] = int16(30000 * math.Sin(2*math.Pi*300*float64(i)/16000))
	}
	g.apply(loud)
	for _, s := range loud {
		if s == math.MaxInt16 || s == math.MinInt16 {
			t.Fatal("loud speech clipped")
		}
	}
}
