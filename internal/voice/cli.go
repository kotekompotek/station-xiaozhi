package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const help = `station-xiaozhi ` + Version + `: voice assistant for Yandex Station (XiaoZhi)

Usage:
  station-xiaozhi run        start the assistant (normally done by the installer)
  station-xiaozhi bind       show the binding code and wait until it is entered at xiaozhi.me
  station-xiaozhi version
  station-xiaozhi help

Say "Яндекс" to talk to the assistant; "Алиса" stays native and always has priority.
`

// bindPoll is how often bind asks the server whether the code was entered.
const bindPoll = 5 * time.Second

const bindTimeout = 10 * time.Minute

// bindRepeat is how often the Station reads the binding code aloud while waiting.
const bindRepeat = 45 * time.Second

func Run(ctx context.Context, args []string, _ *os.File, out, _ io.Writer) error {
	if len(args) == 0 {
		return runStation(ctx, out)
	}
	if len(args) > 1 {
		return fmt.Errorf("%s does not accept arguments", args[0])
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, err := io.WriteString(out, help)
		return err
	case "version", "--version":
		_, err := fmt.Fprintln(out, Version)
		return err
	case "run":
		return runStation(ctx, out)
	case "bind":
		return runBind(ctx, out)
	default:
		return errors.New("unknown command; run station-xiaozhi help")
	}
}

// runBind waits until this Station is bound to an account at xiaozhi.me.
func runBind(ctx context.Context, out io.Writer) error {
	identity, err := loadIdentity(identityPath())
	if err != nil {
		return err
	}
	// adb may leave this running on the Station after the computer gives up waiting.
	ctx, cancel := context.WithTimeoutCause(ctx, bindTimeout,
		errors.New("код не введён за 10 минут; станция продиктует его снова, если сказать «Яндекс»"))
	defer cancel()
	shown, offline := "", false
	var spoken time.Time
	for {
		server, err := fetchServer(ctx, identity)
		switch {
		case ctx.Err() != nil:
			return context.Cause(ctx)
		case err != nil:
			if !offline {
				fmt.Fprintln(out, "Нет связи с сервером XiaoZhi, повторяю...", err)
			}
			offline = true
		case server.ActivationCode == "":
			fmt.Fprintln(out, "Станция привязана к xiaozhi.me.")
			return nil
		case server.ActivationCode != shown:
			shown, offline = server.ActivationCode, false
			fmt.Fprintf(out, "\n  Код привязки: %s\n\n"+
				"Откройте https://xiaozhi.me, войдите, нажмите «Add Device» и введите этот код.\n"+
				"Станция продиктует его вслух. Жду привязки "+
				"(Ctrl+C — продолжить позже: код можно будет снова услышать, сказав станции «Яндекс»)...\n", shown)
			spoken = time.Time{}
		}
		if shown != "" && !offline && time.Since(spoken) >= bindRepeat {
			_ = playCue(ctx, bindingSpeech(shown))
			spoken = time.Now()
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(bindPoll):
		}
	}
}
