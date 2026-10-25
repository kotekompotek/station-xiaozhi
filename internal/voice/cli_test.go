package voice

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunOptions(t *testing.T) {
	var out bytes.Buffer
	if err := Run(t.Context(), []string{"help"}, nil, &out, &out); err != nil || !strings.Contains(out.String(), "bind") {
		t.Fatal("help did not describe binding", err, out.String())
	}
	out.Reset()
	if err := Run(t.Context(), []string{"version"}, nil, &out, &out); err != nil || strings.TrimSpace(out.String()) != Version {
		t.Fatal("version", err, out.String())
	}
	for _, args := range [][]string{{"run", "--voice=orbit"}, {"auth"}, {"bind", "now"}} {
		if err := Run(t.Context(), args, nil, &out, &out); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
}
